package tests

import (
	"int/tasks"
	"int/util"
	"os"

	"github.com/goyek/goyek/v2"
	"golang.org/x/exp/maps"
)

func TaskBuildPipeTests(baseConfig tasks.KV) []goyek.Task {

	testName := "pipes"
	namespace := "nginx-" + testName

	config := tasks.KV{
		"bucket":    "mcs-backup-" + namespace,
		"namespace": namespace,
	}

	maps.Copy(config, baseConfig)

	env := tasks.KV{
		"AWS_ACCESS_KEY_ID":     baseConfig["minioUser"],
		"AWS_SECRET_ACCESS_KEY": baseConfig["minioPassword"],
		"RESTIC_PASSWORD":       "some-secret-password",
		"RETENTION_POLICY":      "{last: 1}",
		"RESTIC_REPOSITORY":     "s3:http://minio.minio.svc:9000/" + config["bucket"] + "/test",
		"BACKUP_PATHS":          "tmp-archive",
		"BACKUP_ROOT":           "/mnt/linux",
		"PIPE_IN_SCRIPT":        "/scripts/pipe-in.sh",
		"PIPE_OUT_SCRIPT":       "/scripts/pipe-out.sh",
		"MCS_BACKUP_DEBUG":      os.Getenv("MCS_BACKUP_DEBUG"),
	}

	backupRoot := env["BACKUP_ROOT"]
	prefix := "exec svc/nginx -c mcs-backup -- "

	return []goyek.Task{buildParameterTestTask(
		testName,
		"test "+testName,
		config,
		env, util.Steps{
			{
				Log:         "pre backup: number of files",
				Kubectl:     prefix + "find %v -type f",
				Args:        util.Args{backupRoot},
				ExpectLines: testDatasetFiles,
				FilterLines: backupRoot,
			},
			{
				Log:     "mcs-backup: run",
				Kubectl: prefix + "mcs-backup backup",
				Hint:    "Did the pipe-in script fail?",
			},
			{
				Log:         "backup: number of files",
				Kubectl:     prefix + "restic ls latest --long",
				ExpectLines: 1,
				FilterLines: "^-r.+/stdin$",
				Hint:        "Did the pipe-in script run correctly?",
			},
			{
				Log:     "remove files",
				Kubectl: prefix + "find %v -mindepth 1 -delete",
				Args:    util.Args{backupRoot},
			},
			{
				Log:         "no files remaining",
				Kubectl:     prefix + "find %v -type f",
				Args:        util.Args{backupRoot},
				ExpectLines: -1,
			},
			{
				Log:     "mcs-backup: restore",
				Kubectl: prefix + "mcs-backup restore latest",
			},
			{
				Log:         "post-backup: number of files",
				Kubectl:     prefix + "find %v -type f",
				Args:        util.Args{backupRoot},
				ExpectLines: testDatasetFiles,
				FilterLines: backupRoot,
				Hint:        "Did the pipe-out script run?",
			},
		},
	)}
}
