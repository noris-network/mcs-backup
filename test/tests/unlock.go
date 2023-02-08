package tests

import (
	"int/tasks"
	"int/util"
	"os"

	"github.com/goyek/goyek/v2"
	"golang.org/x/exp/maps"
)

type AutoUnlockTest struct {
	Name string
	Envs tasks.KV
}

// ohne env

func TaskBuildUnlockTests(baseConfig tasks.KV) []goyek.Task {

	tests := []goyek.Task{}

	testName := "unlock"
	namespace := "nginx-" + testName

	config := tasks.KV{
		"bucket":    "mcs-backup-" + namespace,
		"namespace": namespace,
	}

	maps.Copy(config, baseConfig)

	env0 := tasks.KV{
		"AWS_ACCESS_KEY_ID":     baseConfig["minioUser"],
		"AWS_SECRET_ACCESS_KEY": baseConfig["minioPassword"],
		"RESTIC_PASSWORD":       "some-secret-password",
		"RETENTION_POLICY":      "{last: 10}",
		"RESTIC_REPOSITORY":     "s3:http://minio.minio.svc:9000/" + config["bucket"] + "/repo1",
		"PIPE_IN_SCRIPT":        "/scripts/pipe-in-kill-once.sh",
		"PIPE_OUT_SCRIPT":       "/scripts/pipe-out-discard.sh",
		"MCS_BACKUP_DEBUG":      os.Getenv("MCS_BACKUP_DEBUG"),
	}

	prefix := "exec svc/nginx -c mcs-backup -- "

	prepare := util.Steps{
		{
			Log:         "let pipe-in script kill restic and force a stale lock",
			Kubectl:     prefix + "mcs-backup backup",
			IgnoreError: true,
		},
		{
			Log:         "repository locked?",
			Kubectl:     prefix + "restic forget",
			ExpectMatch: "repository is already locked",
			IgnoreError: true,
		},
	}

	tests = append(tests, buildParameterTestTask(
		testName,
		"test "+testName,
		config,
		env0,
		append(prepare, util.Steps{
			{
				Log:         "no auto-unlock",
				Kubectl:     prefix + "mcs-backup backup",
				ExpectMatch: "AUTO_UNLOCK_AFTER not set",
				IgnoreError: true,
			},
		}...),
	))

	//------------------------------------------------------------

	testName = "unlock-late"
	namespace = "nginx-" + testName

	config = maps.Clone(config)
	config["namespace"] = namespace

	env1 := maps.Clone(env0)
	env1["AUTO_UNLOCK_AFTER"] = "5m"

	tests = append(tests, buildParameterTestTask(
		testName,
		"test "+testName,
		config,
		env1,
		append(prepare, util.Steps{
			{
				Log:         "late auto-unlock",
				Kubectl:     prefix + "mcs-backup backup",
				ExpectMatch: "lock needs to be stale for at least 5m0s to be automatically removed",
				IgnoreError: true,
			},
		}...),
	))

	//------------------------------------------------------------

	testName = "unlock-early"
	namespace = "nginx-" + testName

	config = maps.Clone(config)
	config["namespace"] = namespace

	env2 := maps.Clone(env0)
	env2["AUTO_UNLOCK_AFTER"] = "1ms"

	tests = append(tests, buildParameterTestTask(
		testName,
		"test "+testName,
		config,
		env2,
		append(prepare, util.Steps{
			{
				Log:         "early auto-unlock",
				Kubectl:     prefix + "mcs-backup backup",
				ExpectMatch: "successfully removed locks",
			},
		}...),
	))

	return tests
}
