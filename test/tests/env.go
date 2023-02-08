package tests

import (
	"int/tasks"
	"int/util"
	"regexp"

	"github.com/goyek/goyek/v2"
	"golang.org/x/exp/maps"
)

func TaskBuildEnvTests(baseConfig tasks.KV) []goyek.Task {

	tests := []goyek.Task{}

	testName := "env"
	namespace := "nginx-" + testName

	config := tasks.KV{
		"bucket":    "mcs-backup-" + namespace,
		"namespace": namespace,
	}

	maps.Copy(config, baseConfig)

	env0 := tasks.KV{
		"AWS_ACCESS_KEY_ID":      baseConfig["minioUser"],
		"AWS_SECRET_ACCESS_KEY":  baseConfig["minioPassword"],
		"RESTIC_PASSWORD":        "some-secret-password",
		"RETENTION_POLICY":       "{last: 1}",
		"RESTIC_REPOSITORY_BASE": "s3:http://minio.minio.svc:9000/" + config["bucket"],
		"RESTIC_REPOSITORY_PATH": "repo1",
		"METRICS_LABELS":         `{"namespace":"` + namespace + `","service":"mcs-backup"}`,
	}

	prefix := "exec svc/nginx -c mcs-backup -- "

	tests = append(tests, buildParameterTestTask(
		testName,
		"test environment handling",
		config,
		env0, util.Steps{
			{
				Log:         "check log for missing schedule",
				Kubectl:     "logs svc/nginx -c mcs-backup",
				ExpectMatch: "no cron schedule found",
			},
			{
				Log:         "check log for metrics labels (1/2)",
				Kubectl:     "logs svc/nginx -c mcs-backup",
				ExpectMatch: "- namespace: " + namespace,
			},
			{
				Log:         "check log for metrics labels (2/2)",
				Kubectl:     "logs svc/nginx -c mcs-backup",
				ExpectMatch: "- service: mcs-backup",
			},
			{
				Log:         "check RESTIC_REPOSITORY_{BASE,PATH}",
				Kubectl:     prefix + "env",
				FilterLines: "^RESTIC_REPOSITORY_",
				ExpectLines: 2,
			},
			{
				Log:         "check absence of RESTIC_REPOSITORY",
				Kubectl:     prefix + "env",
				FilterLines: "^RESTIC_REPOSITORY=",
				ExpectLines: -1,
			},
			{
				Log:         "check RESTIC_REPOSITORY",
				Kubectl:     prefix + "mcs-backup env",
				ExpectMatch: `^RESTIC_REPOSITORY="` + env0["RESTIC_REPOSITORY_BASE"] + "/" + env0["RESTIC_REPOSITORY_PATH"] + `"$`,
			},
			{
				Log:         "check absence of RESTIC_REPOSITORY_*",
				Kubectl:     prefix + "mcs-backup env",
				FilterLines: "^RESTIC_REPOSITORY_",
				ExpectLines: -1,
			},
			{
				Log:         "check AWS_ACCESS_KEY_ID",
				Kubectl:     prefix + "mcs-backup env",
				FilterLines: `^AWS_ACCESS_KEY_ID="` + env0["AWS_ACCESS_KEY_ID"] + `"`,
				ExpectLines: 1,
			},
			{
				Log:         "check AWS_SECRET_ACCESS_KEY",
				Kubectl:     prefix + "mcs-backup env",
				FilterLines: `^AWS_SECRET_ACCESS_KEY="` + env0["AWS_SECRET_ACCESS_KEY"] + `"`,
				ExpectLines: 1,
			},
			{
				Log:         "check RESTIC_PASSWORD",
				Kubectl:     prefix + "mcs-backup env",
				FilterLines: `^RESTIC_PASSWORD="` + env0["RESTIC_PASSWORD"] + `"`,
				ExpectLines: 1,
			},
		},
	))

	//------------------------------------------------------------

	testName = "env-cron"
	namespace = "nginx-" + testName

	config = maps.Clone(config)
	config["namespace"] = namespace

	env1 := maps.Clone(env0)
	env1["CRON_SCHEDULE"] = "*/20 * * * 1-5"

	tests = append(tests, buildParameterTestTask(
		testName,
		"test cron schedule from environment",
		config,
		env1, util.Steps{
			{
				Log:         "check log for cron schedule",
				Kubectl:     "logs svc/nginx -c mcs-backup",
				ExpectMatch: regexp.QuoteMeta(`found cron schedule("` + env1["CRON_SCHEDULE"] + `") in environment`),
			},
		},
	))

	//------------------------------------------------------------

	testName = "env-cron-file"
	namespace = "nginx-" + testName

	config = maps.Clone(config)
	config["namespace"] = namespace

	env2 := maps.Clone(env0)
	env2["CRON_SCHEDULE_FILE"] = "/cron/every-minute"

	tests = append(tests, buildParameterTestTask(
		testName,
		"test cron schedule from file",
		config,
		env2, util.Steps{
			{
				Log:         "check log for cron schedule",
				Kubectl:     "logs svc/nginx -c mcs-backup",
				ExpectMatch: regexp.QuoteMeta(`found cron schedule file "/cron/every-minute" ("* * * * *")`),
			},
		},
	))

	return tests
}
