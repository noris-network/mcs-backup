package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/noris-network/mcs-backup/v2/internal/config"
	"github.com/noris-network/mcs-backup/v2/internal/envexport"
	"github.com/noris-network/mcs-backup/v2/internal/hooks"
	"github.com/noris-network/mcs-backup/v2/internal/restic"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// NewFromConfig builds an Engine for cfg: the restic repositories, the hook
// runner and the logger.
func NewFromConfig(cfg *config.Config, log *slog.Logger) *Engine {
	opts := restic.Options{Bin: cfg.Restic.Bin, Env: cfg.ResticEnviron(), Dir: cfg.Root, GlobalArgs: cfg.Restic.Global, Logger: log}
	o := Options{
		Config: cfg,
		Main:   restic.New(opts),
		Hooks:  &hooks.Runner{Dir: cfg.Root, Env: cfg.Environ()},
		Logger: log,
	}
	if cfg.Copy != nil {
		opts.Env = SecondEnv(cfg)
		o.Second = restic.New(opts)
	}
	return New(o)
}

// SecondEnv returns the restic environment for the secondary repository: the
// environment of the main repository with the repository and password
// replaced. The storage credentials stay the same, which is why both
// repositories need a backend that accepts them (see the documentation).
func SecondEnv(cfg *config.Config) []string {
	env := slices.DeleteFunc(cfg.ResticEnviron(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "RESTIC_REPOSITORY", "RESTIC_REPOSITORY_FILE", "RESTIC_PASSWORD", "RESTIC_PASSWORD_FILE", "RESTIC_PASSWORD_COMMAND":
			return true
		}
		return strings.HasPrefix(name, "RESTIC_FROM_")
	})
	env = append(env, "RESTIC_REPOSITORY="+cfg.Copy.Repository)
	if cfg.Copy.PasswordFile != "" {
		return append(env, "RESTIC_PASSWORD_FILE="+cfg.Copy.PasswordFile)
	}
	return append(env, "RESTIC_PASSWORD="+cfg.Copy.Password)
}

// EnvInfo describes how to run restic against the main repository elsewhere.
// Secrets are masked unless reveal is set.
func EnvInfo(cfg *config.Config, reveal bool) service.EnvInfo {
	oldest := fmt.Sprintf("%d.%d", MinResticVersion.Major, MinResticVersion.Minor)
	return envexport.Build(cfg.ResticEnviron(), cfg.Restic.Global, oldest, reveal)
}

// ProbeResult is what Probe found out about one repository.
type ProbeResult struct {
	Repo string // "main" or "copy"
	ID   string
	// Missing: the repository does not exist yet; the service creates it at its first start.
	Missing bool
	// Err is why the repository cannot be used.
	Err error
}

// Probe checks that restic works and that both repositories can be used:
// they exist (or can be created), the password is right and a lock can be
// taken and released. The last check shows early whether an append-only
// storage lets restic delete its locks.
func (e *Engine) Probe(ctx context.Context) (restic.Version, []ProbeResult, error) {
	v, err := e.CheckVersion(ctx)
	if err != nil {
		return v, nil, err
	}
	repos := []struct {
		name string
		repo *restic.Repo
	}{{"main", e.main}}
	if e.second != nil {
		repos = append(repos, struct {
			name string
			repo *restic.Repo
		}{"copy", e.second})
	}
	var out []ProbeResult
	for _, r := range repos {
		res := ProbeResult{Repo: r.name}
		c, err := r.repo.Config(ctx)
		switch {
		case errors.Is(err, restic.ErrRepoMissing):
			res.Missing = true
		case err != nil:
			res.Err = err
		default:
			res.ID = c.ID
			if err := r.repo.LockCheck(ctx); err != nil {
				res.Err = errors.Join(errors.New("cannot take and release a lock (on append-only storage, deleting under locks/ must be allowed)"), err)
			}
		}
		out = append(out, res)
	}
	return v, out, nil
}
