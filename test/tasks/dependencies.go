package tasks

import (
	"os/exec"

	"github.com/goyek/goyek/v2"
)

func TaskCheckDependencies() goyek.Task {
	return goyek.Task{
		Name:  "check-dependencies",
		Usage: "check dependencies for running tests and demo",
		Action: func(tf *goyek.A) {
			requiredExecutables := []string{
				"curl",
				"docker",
				"k3d",
				"kubectl",
			}
			for _, exe := range requiredExecutables {
				if path, err := exec.LookPath(exe); err != nil {
					tf.Errorf("missing %q", exe)
				} else {
					tf.Logf("found %q in %q", exe, path)
				}
			}
		},
	}
}
