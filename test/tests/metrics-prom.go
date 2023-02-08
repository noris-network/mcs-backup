package tests

import (
	"int/tasks"
	"int/util"

	"github.com/goyek/goyek/v2"
	"golang.org/x/exp/maps"
)

func TaskBuildPromMetricsTests(baseConfig tasks.KV) []goyek.Task {

	tests := []goyek.Task{}

	testName := "metrics-prom"
	namespace := "nginx-" + testName

	config := tasks.KV{
		"bucket":    "mcs-backup-" + namespace,
		"namespace": namespace,
	}

	maps.Copy(config, baseConfig)

	env := tasks.KV{
		"AWS_ACCESS_KEY_ID":      baseConfig["minioUser"],
		"AWS_SECRET_ACCESS_KEY":  baseConfig["minioPassword"],
		"RESTIC_PASSWORD":        "some-secret-password",
		"RETENTION_POLICY":       "{last: 1}",
		"RESTIC_REPOSITORY_BASE": "s3:http://minio.minio.svc:9000/" + config["bucket"],
		"RESTIC_REPOSITORY_PATH": "repo1",
		"METRICS_LABELS":         `{"namespace":"` + namespace + `","service":"mcs-backup"}`,
	}

	prefix := "exec svc/nginx -c mcs-backup -- "
	mcsMetrics := 40

	tests = append(tests, buildParameterTestTask(
		testName,
		"test "+testName,
		config,
		env, util.Steps{
			{
				Log:     "mcs-backup: create backup",
				Kubectl: prefix + "mcs-backup backup",
			},
			{
				Log:         "check metrics with 'mcsbackup_' prefix",
				Kubectl:     prefix + "curl -s localhost:9000/metrics",
				FilterLines: "^mcsbackup_",
				ExpectLines: mcsMetrics,
			},
			{
				Log:         "check metrics with label 'namespace'",
				Kubectl:     prefix + "curl -s localhost:9000/metrics",
				FilterLines: `namespace="` + namespace + `"`,
				ExpectLines: mcsMetrics,
			},
			{
				Log:         "check metrics with label 'service'",
				Kubectl:     prefix + "curl -s localhost:9000/metrics",
				FilterLines: `service="mcs-backup"`,
				ExpectLines: mcsMetrics,
			},
			{
				Log:         "check metrics with label 'repository_id'",
				Kubectl:     prefix + "curl -s localhost:9000/metrics",
				FilterLines: `repository_id="\w+"`,
				ExpectLines: mcsMetrics,
			},
		},
	))

	return tests
}
