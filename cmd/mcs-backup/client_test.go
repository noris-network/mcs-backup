package main

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/noris-network/mcs-backup/v2/internal/service"
	"github.com/noris-network/mcs-backup/v2/internal/servicetest"
)

// sourced is a service that remembers the source it was asked to report.
type sourced struct {
	*servicetest.Fake
	source string
}

func (s *sourced) As(source string) service.Service {
	s.source = source
	return s.Fake
}

func TestServiceAsReportsTheSource(t *testing.T) {
	for source, run := range map[string]func(*app, *cobra.Command){
		service.SourceCLI: func(a *app, cmd *cobra.Command) { _, closeSvc := a.service(cmd); closeSvc() },
		service.SourceTUI: func(a *app, cmd *cobra.Command) { _, closeSvc := a.serviceAs(cmd, service.SourceTUI); closeSvc() },
		service.SourceMCP: func(a *app, cmd *cobra.Command) { _, closeSvc := a.serviceAs(cmd, service.SourceMCP); closeSvc() },
	} {
		s := &sourced{Fake: &servicetest.Fake{}}
		a := &app{getenv: func(string) string { return "/tmp/x.sock" }, dial: func(string) service.Service { return s }}
		cmd := &cobra.Command{}
		cmd.Flags().String("socket", "", "")
		run(a, cmd)
		if s.source != source {
			t.Errorf("the connection reports %q, want %q", s.source, source)
		}
	}
}
