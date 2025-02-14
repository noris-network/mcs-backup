package tests

import (
	"fmt"
	"os"
	"time"

	"int/files"
	"int/tasks"
	"int/util"

	"github.com/goyek/goyek/v2"
)

type TestFunc func() error

var (
	testDatasetFiles = 561
	debug            = os.Getenv("MCS_BACKUP_DEBUG") == "true"
)

func buildParameterTestTask(name, usage string, config, env tasks.KV, steps []util.Step) goyek.Task {
	return goyek.Task{
		Name:  "test-" + name,
		Usage: usage,
		Action: func(tf *goyek.A) {
			namespace := config["namespace"]

			// run test steps
			if err := util.RunSteps(tf, namespace, append([]util.Step{
				{
					Kubectl:     "create namespace " + namespace,
					IgnoreError: true,
				},
				{
					Log: "deploy configmap 'pv-backup-env'",
					Func: func() error {
						return tasks.ApplyConfigMap(namespace, "pv-backup-env", env)
					},
				},
				{
					Log: "deploy configmap 'scripts'",
					Func: func() error {
						return tasks.ApplyYamlTmpl(files.ScriptsYaml, config)
					},
				},
				{
					Log: "deploy configmap 'cron'",
					Func: func() error {
						return tasks.ApplyYamlTmpl(files.CronYaml, config)
					},
				},
				{
					Log: "deploy deployment and service 'nginx'",
					Func: func() error {
						return tasks.ApplyYamlTmpl(files.NginxYaml, config)
					},
				},
				{
					Log:     "wait until pod is ready",
					Kubectl: "wait --for=condition=ready --timeout=200s pod -l app=nginx",
				},
				{
					Log:     "wait for service to be ready",
					Kubectl: "exec svc/nginx -c mcs-backup -- mcs-backup backup --status",
					UntilOK: true,
				},
				{
					Sleep: time.Second,
				},
			}, steps...)...); err != nil {
				tf.Errorf("Error: %v", err)
			}

			// run cleanup
			if os.Getenv("SKIP_CLEANUP") != "true" {
				util.RunSteps(tf, namespace, util.Steps{
					{
						Kubectl: "delete deployment nginx",
					},
					{
						Log:     fmt.Sprintf("cleanup backup bucket %q", config["bucket"]),
						Kubectl: "exec -n minio svc/minio -c mc -- mc rm --recursive --force s3/" + config["bucket"],
					},
					{
						Kubectl:     "delete namespace " + namespace,
						IgnoreError: true,
					},
				}...)
			}
		},
		Deps: []*goyek.DefinedTask{},
	}
}
