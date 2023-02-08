package tests

import (
	"fmt"
	"int/tasks"
	"int/util"
	"strings"
	"time"

	"github.com/bitfield/script"
	"github.com/goyek/goyek/v2"
	"golang.org/x/exp/maps"
)

func TaskBuildLokiLogsTests(baseConfig tasks.KV) []goyek.Task {

	tests := []goyek.Task{}

	testName := "logs-loki"
	namespace := "nginx-" + testName

	config := tasks.KV{
		"bucket":    "mcs-backup-" + namespace,
		"namespace": namespace,
	}

	maps.Copy(config, baseConfig)

	labelValue := fmt.Sprintf("test-%v", time.Now().Unix())

	env := tasks.KV{
		"AWS_ACCESS_KEY_ID":      baseConfig["minioUser"],
		"AWS_SECRET_ACCESS_KEY":  baseConfig["minioPassword"],
		"RESTIC_PASSWORD":        "some-secret-password",
		"RETENTION_POLICY":       "{last: 1}",
		"RESTIC_REPOSITORY_BASE": "s3:http://minio.minio.svc:9000/" + config["bucket"],
		"RESTIC_REPOSITORY_PATH": "repo1",
		"METRICS_LABELS":         `{"testlabel":"` + labelValue + `","namespace":"` + namespace + `"}`,
		"LOKI_URL":               "http://loki.backup-monitoring.svc:3100",
	}

	prefix := "exec svc/nginx -c mcs-backup -- "

	tests = append(tests, buildParameterTestTask(
		testName,
		"test "+testName,
		config,
		env, util.Steps{
			{
				Log:         "check loki init message",
				Kubectl:     "logs svc/nginx -c mcs-backup",
				ExpectMatch: `will send logs to "http://loki.backup-monitoring.svc:3100/api/prom/push"`,
			},
			{
				Log:     "create backup",
				Kubectl: prefix + "mcs-backup backup",
			},
			{
				Log:   "give mcs-backup some time to upload logs",
				Sleep: 5 * time.Second,
			},
			{
				Log: "check loki logs",
				Func: func() error {
					cmd := fmt.Sprintf(
						"kubectl exec -n %v svc/nginx -c nginx -- "+
							"curl -s -G http://loki.backup-monitoring.svc:3100/loki/api/v1/query_range "+
							`--data-urlencode 'query={testlabel="%v"}' `+
							"--data-urlencode limit=1 "+
							"--data-urlencode start=%v",
						namespace, labelValue, time.Now().Add(-10*time.Second).Unix(),
					)
					if debug {
						util.PrintDebug("CMD", cmd)
					}
					line, err := script.Exec(cmd).
						JQ(".data.result[0].values | .[] | .[1]").
						String()
					if err != nil {
						return err
					}
					if !strings.Contains(line, "backup finished") {
						return fmt.Errorf("expected line not found (found %q)", line)
					}
					return nil
				},
			},
		},
	))

	return tests
}
