package tests

import (
	"int/tasks"
	"int/util"

	"github.com/goyek/goyek/v2"
	"golang.org/x/exp/maps"
)

func TaskBuildInfluxMetricsTests(baseConfig tasks.KV) []goyek.Task {

	tests := []goyek.Task{}

	testName := "metrics-influx"
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
		"INFLUXDB_URL":           "http://influxdb.backup-monitoring.svc:8086",
		"INFLUXDB_DATABASE":      "backup-metrics",
		"INFLUXDB_TOKEN":         "mcs-backup-demo-auth-token",
		"INFLUXDB_ORG":           "mcs-backup",
	}

	prefix := "exec svc/nginx -c mcs-backup -- "
	infuxQuery := `from(bucket: "backup-metrics") |> range(start: -2s) |> filter(fn: (r) => r["namespace"] == "nginx-metrics-influx")`

	tests = append(tests, buildParameterTestTask(
		testName,
		"test "+testName,
		config,
		env, util.Steps{
			{
				Log:         "check influxdb check",
				Kubectl:     "logs svc/nginx -c mcs-backup",
				ExpectMatch: `check influxdb "http://influxdb.backup-monitoring.svc:8086" db="backup-metrics"`,
			},
			{
				Log:     "create backup",
				Kubectl: prefix + "mcs-backup backup",
			},
			{
				Log:         "check influx metrics",
				Kubectl:     "exec -n backup-monitoring svc/influxdb -- influx query '%s'",
				Args:        util.Args{infuxQuery},
				ExpectMatch: "mcsbackup_meta.+" + namespace,
			},
		},
	))

	return tests
}
