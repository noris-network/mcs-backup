package demo

import (
	"int/tasks"
	"int/util"

	"github.com/goyek/goyek/v2"
	"golang.org/x/exp/maps"
)

func TaskDemo(baseConfig tasks.KV) []goyek.Task {

	//namespace := "demo"

	return []goyek.Task{{
		Name:  "demo",
		Usage: "demo setup",
		Action: func(tf *goyek.A) {

			namespace := "demo"

			config := tasks.KV{
				"bucket":    "mcs-backup-" + namespace,
				"namespace": namespace,
			}

			maps.Copy(config, baseConfig)

			env := tasks.KV{
				"AWS_ACCESS_KEY_ID":     baseConfig["minioUser"],
				"AWS_SECRET_ACCESS_KEY": baseConfig["minioPassword"],
				"RESTIC_PASSWORD":       "some-secret-password",
				"RETENTION_POLICY":      "{last: 10}",
				"RESTIC_REPOSITORY":     "s3:http://minio.minio.svc:9000/" + config["bucket"] + "/repo1",
				"CRON_SCHEDULE":         "* * * * *",
				"BACKUP_ROOT":           "/mnt/linux",
			}

			// setup demo
			if err := util.RunSteps(tf, namespace, ([]util.Step{
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
					Log: "deploy demo",
					Func: func() error {
						return tasks.RunKustomize("create", "test/deploy/demo")
					},
				},
				{
					Log: "deploy grafana",
					Func: func() error {
						return tasks.RunKustomize("create", "test/deploy/grafana")
					},
				},
			})...); err != nil {
				tf.Errorf("Error: %v", err)
			}
		},
	}}
}
