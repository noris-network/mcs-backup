package main

import (
	"strings"
	"testing"
)

func TestKcPrintsTheKubectlLines(t *testing.T) {
	c := newCLI(t)
	c.vars = map[string]string{"KUBERNETES_SERVICE_HOST": "10.0.0.1", "HOSTNAME": "shop-0", "POD_NAMESPACE": "shop", "MCS_BACKUP_WEB_ADDR": "127.0.0.1:9090"}
	if code := c.run("kc"); code != 0 {
		t.Fatalf("exit %d: %s", code, c.err())
	}
	out := c.out()
	for _, want := range []string{
		"kubectl exec -it -n shop shop-0 -c mcs-backup -- mcs-backup tui ",
		"kubectl exec -n shop shop-0 -c mcs-backup -- mcs-backup status ",
		"kubectl port-forward -n shop pod/shop-0 9090:9090 ",
		"kubectl exec -n shop shop-0 -c mcs-backup -- mcs-backup web ",
		"kubectl exec -i -n shop shop-0 -c mcs-backup -- mcs-backup mcp ",
		"kubectl logs -f -n shop shop-0 -c mcs-backup ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output lacks %q:\n%s", want, out)
		}
	}
	// every line can be pasted as it is: what it is for is a shell comment,
	// and the comments stand under each other
	col := -1
	for l := range strings.SplitSeq(strings.TrimRight(out, "\n"), "\n") {
		i := strings.Index(l, "  # ")
		if !strings.HasPrefix(l, "kubectl ") || i < 0 {
			t.Errorf("line %q is not a command with a comment", l)
			continue
		}
		if col >= 0 && i != col {
			t.Errorf("the comments do not stand under each other:\n%s", out)
		}
		col = i
	}
	// the port forward comes before the command that prints the login link
	if strings.Index(out, "port-forward") > strings.Index(out, "mcs-backup web") {
		t.Errorf("the port forward comes after the web command:\n%s", out)
	}

	// another container name, and the defaults of the web address
	c.vars = map[string]string{"KUBERNETES_SERVICE_HOST": "10.0.0.1", "HOSTNAME": "shop-0", "POD_NAMESPACE": "shop", "MCS_BACKUP_CONTAINER": "backup"}
	if code := c.run("kc"); code != 0 || !strings.Contains(c.out(), "shop-0 -c backup -- mcs-backup tui") || !strings.Contains(c.out(), "pod/shop-0 8484:8484") {
		t.Errorf("exit %d:\n%s", code, c.out())
	}

	if code := c.run("kc", "--json"); code != 0 {
		t.Fatalf("exit %d: %s", code, c.err())
	}
	for _, want := range []string{`"namespace": "shop"`, `"pod": "shop-0"`, `"container": "backup"`, `"for": "the TUI"`, `"command": "kubectl exec -it -n shop shop-0 -c backup -- mcs-backup tui"`} {
		if !strings.Contains(c.out(), want) {
			t.Errorf("the JSON lacks %s:\n%s", want, c.out())
		}
	}
}

func TestKcOutsideAPod(t *testing.T) {
	c := newCLI(t)
	if code := c.run("kc"); code != exitCodeUsage || !strings.Contains(c.err(), "not in a Kubernetes pod") {
		t.Errorf("exit %d, stderr %q", code, c.err())
	}
}
