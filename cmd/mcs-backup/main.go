// Command mcs-backup is a restic wrapper that runs scheduled backups,
// restores and repository maintenance, usually as a sidecar in Kubernetes.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // time zones for TZ and cron schedules in minimal images
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a := newApp()
	code := a.run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}
