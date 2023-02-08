package tests

import (
	"int/tasks"
	"int/util"

	"github.com/goyek/goyek/v2"
	"golang.org/x/exp/maps"
)

func TaskBuildHookTests(baseConfig tasks.KV) []goyek.Task {

	testName := "hooks"
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
		"PRE_BACKUP_SCRIPT":     "/scripts/pre-backup.sh",
		"POST_BACKUP_SCRIPT":    "/scripts/post-backup.sh",
		"PRE_RESTORE_SCRIPT":    "/scripts/pre-restore.sh",
		"POST_RESTORE_SCRIPT":   "/scripts/post-restore.sh",
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
			},
			{
				Log:         "backup: number of files",
				Kubectl:     prefix + "mcs-backup restic ls latest --long",
				ExpectLines: 1,
				FilterLines: "^-r",
				Hint:        "Did the pre-backup-script run?",
			},
			{
				Log:         "post backup: number of files",
				Kubectl:     prefix + "find %v -type f",
				Args:        util.Args{backupRoot},
				ExpectLines: testDatasetFiles,
				FilterLines: backupRoot,
				Hint:        "Did the post-backup-script run?",
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
				Log:         "create unwanted file",
				Kubectl:     prefix + "touch %v/mcs-backup.zzz",
				Args:        util.Args{backupRoot},
				ExpectLines: -1,
			},
			{
				Log:     "mcs-backup: restore",
				Kubectl: prefix + "mcs-backup restore latest",
			},
			{
				Log:         "post backup: post backup script",
				Kubectl:     prefix + "find %v -type f -name mcs-backup.zzz",
				Args:        util.Args{backupRoot},
				ExpectLines: -1,
				FilterLines: backupRoot,
				Hint:        "Did the pre-restore-script run?",
			},
			{
				Log:         "post-backup: number of files",
				Kubectl:     prefix + "find %v -type f",
				Args:        util.Args{backupRoot},
				ExpectLines: testDatasetFiles,
				FilterLines: backupRoot,
				Hint:        "Did the post-restore-script run?",
			},
		},
	)}
}
