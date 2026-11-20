package main

import (
	"cmp"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/noris-network/mcs-backup/v2/internal/webui"
)

// pod is where this mcs-backup runs in Kubernetes.
type pod struct {
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"pod"`
	Container string `json:"container"`
}

// podRef finds the pod this process runs in: the name from HOSTNAME, the
// namespace from POD_NAMESPACE or the service account, the container from
// MCS_BACKUP_CONTAINER (Kubernetes tells a container nothing about its own
// name). ok is false outside Kubernetes.
func (a *app) podRef() (p pod, ok bool) {
	if a.getenv("KUBERNETES_SERVICE_HOST") == "" {
		return p, false
	}
	p.Namespace = a.getenv("POD_NAMESPACE")
	if p.Namespace == "" {
		if ns, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
			p.Namespace = strings.TrimSpace(string(ns))
		}
	}
	p.Name = cmp.Or(a.getenv("HOSTNAME"), "<pod>")
	p.Container = cmp.Or(a.getenv("MCS_BACKUP_CONTAINER"), "mcs-backup")
	return p, true
}

// ns is the namespace option of kubectl, empty if the namespace is not known.
func (p pod) ns() string {
	if p.Namespace == "" {
		return ""
	}
	return " -n " + p.Namespace
}

// exec is a kubectl exec of an mcs-backup command in the container.
func (p pod) exec(flags, args string) string {
	return fmt.Sprintf("kubectl exec%s%s %s -c %s -- mcs-backup %s", flags, p.ns(), p.Name, p.Container, args)
}

// portForward forwards port on the machine of the user to the pod.
func (p pod) portForward(port string) string {
	return fmt.Sprintf("kubectl port-forward%s pod/%s %s:%s", p.ns(), p.Name, port, port)
}

// kcLine is one command line with what it is for.
type kcLine struct {
	For     string `json:"for"`
	Command string `json:"command"`
}

func (a *app) newKcCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "kc",
		Short: "Print the kubectl command lines that reach this pod: TUI, web UI, MCP, log",
		Long: `Print the kubectl command lines that reach this mcs-backup from your machine,
with the namespace, the pod and the container filled in: copy them instead of
typing them. Run it in the pod, for example

  kubectl exec <pod> -c mcs-backup -- mcs-backup kc

The pod is HOSTNAME, the namespace POD_NAMESPACE or the one of the service account,
the container MCS_BACKUP_CONTAINER or mcs-backup: Kubernetes does not tell a
container its own name, so set MCS_BACKUP_CONTAINER if the container is named
differently.`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, ok := a.podRef()
			if !ok {
				return usageError(errors.New("not in a Kubernetes pod (KUBERNETES_SERVICE_HOST is not set): run it in the pod of mcs-backup"))
			}
			_, port, err := net.SplitHostPort(cmp.Or(a.getenv("MCS_BACKUP_WEB_ADDR"), webui.DefaultAddr))
			if err != nil {
				_, port, _ = net.SplitHostPort(webui.DefaultAddr)
			}
			lines := []kcLine{
				{"the TUI", p.exec(" -it", "tui")},
				{"the status", p.exec("", "status")},
				{"the web UI: forward the port first", p.portForward(port)},
				{"then get the login link", p.exec("", "web")},
				{"an AI agent (MCP)", p.exec(" -i", "mcp")},
				{"the log of the service", fmt.Sprintf("kubectl logs -f%s %s -c %s", p.ns(), p.Name, p.Container)},
			}
			if a.wantJSON(cmd) {
				return a.printJSON(struct {
					pod
					Commands []kcLine `json:"commands"`
				}{p, lines})
			}
			w := 0
			for _, l := range lines {
				w = max(w, len(l.Command))
			}
			// the comments make the lines safe to paste as they are
			for _, l := range lines {
				fmt.Fprintf(a.stdout, "%-*s  # %s\n", w, l.Command, l.For)
			}
			return nil
		},
	}
}
