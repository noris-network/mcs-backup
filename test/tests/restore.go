package tests

import (
	"int/tasks"
	"int/util"
	"strconv"

	"github.com/goyek/goyek/v2"
)

type RestoreTest struct {
	Name  string
	Steps util.Steps
}

func TaskBuildRestoreTests(baseConfig tasks.KV) []goyek.Task {

	namespace := "nginx-restore"
	config := tasks.KV{
		"bucket":    "mcs-backup-" + namespace,
		"namespace": namespace,
	}

	env := tasks.KV{
		"AWS_ACCESS_KEY_ID":     baseConfig["minioUser"],
		"AWS_SECRET_ACCESS_KEY": baseConfig["minioPassword"],
		"RESTIC_PASSWORD":       "some-secret-password",
		"RETENTION_POLICY":      "{last: 20}",
		"RESTIC_REPOSITORY":     "s3:http://minio.minio.svc:9000/" + config["bucket"] + "/test",
		"BACKUP_ROOT":           "/mnt/datadir",
		"PRE_RESTORE_SCRIPT":    "/scripts/pre-restore.sh",
	}

	prefix := "exec svc/nginx -c mcs-backup -- "

	backups := util.Steps{
		util.Step{
			Log:     "mkdir datadir",
			Kubectl: prefix + "mkdir /mnt/datadir",
		},
		util.Step{
			Log:     "mcs-backup: create first backup (empty)",
			Kubectl: prefix + "mcs-backup backup",
		},
	}

	dirs := []string{"drivers", "include", "fs"}

	for _, dir := range dirs {
		backups = append(backups,
			util.Step{
				Log:     "add new dir '" + dir + "'",
				Kubectl: prefix + "mv /mnt/linux/%v /mnt/datadir",
				Args:    util.Args{dir},
			},
			util.Step{
				Log:     "mcs-backup: create backup (new dir '" + dir + "')",
				Kubectl: prefix + "mcs-backup backup",
			},
		)
	}


	//files := []int{200, 332, 447}
	files := []int{447, 447, 447} // workaround buggy test (1.22/loopvar)
	backupRoot := env["BACKUP_ROOT"]

	// backups = append(backups,
	// 	util.Step{
	// 		Log:     "sleep..............",
	// 		Kubectl: prefix + "sleep 1d",
	// 	},
	// )

	for idx := range dirs {
		backups = append(backups,
			util.Step{
				Log:     "remove all files in backup root",
				Kubectl: prefix + "find %v -mindepth 1 -delete",
				Args:    util.Args{backupRoot},
			},
			util.Step{
				Log:     "restore snapshot #" + strconv.Itoa(3-idx),
				Kubectl: prefix + `ash -c "mcs-backup restore $(restic snapshots --json | yq -r '.[%v] | .short_id')"`,
				Args:    util.Args{2 - idx},
			},
			util.Step{
				Log:         "post-backup: number of files",
				Kubectl:     prefix + "find %v -type f",
				Args:        util.Args{backupRoot},
				ExpectLines: files[2-idx],
				FilterLines: backupRoot,
			},
		)
	}

	backups = append(backups,
		util.Step{
			Log:     "remove all files in backup root",
			Kubectl: prefix + "find %v -mindepth 1 -delete",
			Args:    util.Args{backupRoot},
		},
		util.Step{
			Log:     "restore snapshot latest",
			Kubectl: prefix + `mcs-backup restore latest`,
		},
		util.Step{
			Log:         "post-backup: number of files",
			Kubectl:     prefix + "find %v -type f",
			Args:        util.Args{backupRoot},
			ExpectLines: files[2],
			FilterLines: backupRoot,
		},
	)

	return []goyek.Task{
		buildParameterTestTask(
			"restore",
			"restore test",
			config,
			env, backups,
		),
	}
}
