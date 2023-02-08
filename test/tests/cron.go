package tests

import (
	"int/tasks"
	"int/util"
	"time"

	"github.com/goyek/goyek/v2"
	"golang.org/x/exp/maps"
)

func TaskBuildCronTests(baseConfig tasks.KV) []goyek.Task {

	tests := []goyek.Task{}

	testName := "cron"
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
		"RETENTION_POLICY":       "{last: 10}",
		"RESTIC_REPOSITORY_BASE": "s3:http://minio.minio.svc:9000/" + config["bucket"],
		"RESTIC_REPOSITORY_PATH": "repo1",
		"CRON_SCHEDULE":          "* * * * *",
	}

	tests = append(tests, buildParameterTestTask(
		testName,
		"test "+testName+" (slow)",
		config,
		env, util.Steps{
			util.Step{
				Log:   "sleep 65s",
				Sleep: 65 * time.Second,
			},
			{
				Log:         "check log for backup activity",
				Kubectl:     "logs svc/nginx -c mcs-backup",
				ExpectMatch: "backup triggered via cron",
			},
			{
				Log:         "check log for backup activity",
				Kubectl:     "logs svc/nginx -c mcs-backup",
				ExpectMatch: "next run:",
			},
		},
	))

	return tests
}
