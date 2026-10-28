package main

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// build is set at link time (-ldflags "-X main.build=<version>"). It is the
// only package-level variable, because the linker needs one.
var build string

// version returns the release version, falling back to the VCS revision
// recorded by the Go toolchain for local builds.
func version() string {
	if build != "" {
		return build
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				return "dev-" + s.Value[:7]
			}
		}
	}
	return "dev"
}

func (a *app) newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the mcs-backup version",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(*cobra.Command, []string) error {
			_, err := fmt.Fprintf(a.stdout, "mcs-backup %s (%s, %s/%s)\n",
				version(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
			return err
		},
	}
}
