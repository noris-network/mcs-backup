package main

import (
	"errors"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/noris-network/mcs-backup/v2/internal/config"
	"github.com/noris-network/mcs-backup/v2/internal/engine"
	"github.com/noris-network/mcs-backup/v2/internal/present"
)

func (a *app) newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Configuration commands", Args: usageArgs(cobra.NoArgs)}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }
	cmd.AddCommand(a.newConfigCheckCmd("check", false))
	return cmd
}

// newConfigCheckCmd returns "config check"; as "info" it always connects.
func (a *app) newConfigCheckCmd(use string, alwaysConnect bool) *cobra.Command {
	var connect bool
	cmd := &cobra.Command{
		Use:   use,
		Short: "Check the configuration and show the effective settings",
		Long: `Read the configuration from the environment, report every problem and show the
effective settings including the defaults. Secrets are not shown.

With --connect restic is run against the repositories: it must exist (or be
creatable), the password must be right and a lock must be possible. That
includes append-only storage, where deleting locks must be allowed.`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.configCheck(cmd, connect || alwaysConnect)
		},
	}
	if !alwaysConnect {
		cmd.Flags().BoolVar(&connect, "connect", false, "also check access to the repositories")
	}
	return cmd
}

// connectReport is the result of the access check in the JSON output.
type connectReport struct {
	ResticVersion string              `json:"restic_version"`
	Repositories  []connectRepository `json:"repositories"`
}

type connectRepository struct {
	Repo    string `json:"repository"`
	ID      string `json:"id,omitempty"`
	Missing bool   `json:"missing,omitzero"`
	Error   string `json:"error,omitempty"`
}

func (a *app) configCheck(cmd *cobra.Command, connect bool) error {
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	settings := cfg.Describe()

	var report *connectReport
	var connectErr error
	if connect {
		report, connectErr = a.probe(cmd, cfg)
	}

	if a.wantJSON(cmd) {
		out := struct {
			Settings []config.Setting `json:"settings"`
			Warnings []string         `json:"warnings,omitempty"`
			Connect  *connectReport   `json:"connect,omitempty"`
			Error    string           `json:"error,omitempty"`
		}{settings, cfg.Warnings, report, ""}
		if connectErr != nil {
			out.Error = connectErr.Error()
		}
		if err := a.printJSON(out); err != nil {
			return err
		}
		if connectErr != nil {
			return &exitError{code: exitCodeError, err: connectErr, silent: true}
		}
		return nil
	}

	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	for _, s := range settings {
		fmt.Fprintf(w, "%s\t%s\t%s\n", s.Name, s.Value, s.Note)
	}
	_ = w.Flush()
	for _, warn := range cfg.Warnings {
		fmt.Fprintf(a.stdout, "warning: %s\n", warn)
	}
	if report != nil {
		fmt.Fprintf(a.stdout, "\nrestic %s\n", report.ResticVersion)
		for _, r := range report.Repositories {
			switch {
			case r.Error != "":
				fmt.Fprintf(a.stdout, "%s repository: NOT USABLE: %s\n", r.Repo, r.Error)
			case r.Missing:
				fmt.Fprintf(a.stdout, "%s repository: does not exist yet, the service creates it at its first start\n", r.Repo)
			default:
				fmt.Fprintf(a.stdout, "%s repository: ok (id %s), a lock can be taken and released\n", r.Repo, present.Short(r.ID))
			}
		}
	}
	if connectErr != nil {
		return &exitError{code: exitCodeError, err: connectErr}
	}
	if !connect {
		fmt.Fprintln(a.stdout, "\nThe configuration is valid. Add --connect to check access to the repositories.")
	}
	return nil
}

// probe checks restic and the repositories.
func (a *app) probe(cmd *cobra.Command, cfg *config.Config) (*connectReport, error) {
	eng := engine.NewFromConfig(cfg, nil)
	v, results, err := eng.Probe(cmd.Context())
	if err != nil {
		return nil, err
	}
	report := &connectReport{ResticVersion: v.String()}
	var errs []error
	for _, r := range results {
		cr := connectRepository{Repo: r.Repo, ID: r.ID, Missing: r.Missing}
		if r.Err != nil {
			cr.Error = r.Err.Error()
			errs = append(errs, fmt.Errorf("%s repository: %w", r.Repo, r.Err))
		}
		report.Repositories = append(report.Repositories, cr)
	}
	if len(errs) > 0 {
		return report, errors.New("a repository cannot be used")
	}
	return report, nil
}
