package tests

import (
	"fmt"
	"int/tasks"
	"int/util"

	"github.com/goyek/goyek/v2"
	"golang.org/x/exp/maps"
)

type PathTest struct {
	Name        string
	Envs        tasks.KV
	ExpectFiles int
}

func TaskBuildPathTests(baseConfig tasks.KV) []goyek.Task {

	tests := []goyek.Task{}

	testDatasets := []PathTest{
		{
			Name: "path-dot",
			Envs: tasks.KV{
				"BACKUP_PATHS":  ".",
				"EXCLUDE_PATHS": "",
				"BACKUP_ROOT":   "/mnt/linux",
			},
			// restic backup -n .
			ExpectFiles: testDatasetFiles,
		},
		{
			Name: "path-root",
			Envs: tasks.KV{
				"BACKUP_PATHS":  ".",
				"EXCLUDE_PATHS": "",
				"BACKUP_ROOT":   "/mnt/linux/drivers",
			},
			// cd drivers; restic backup -n .
			ExpectFiles: 200,
		},
		{
			Name: "path-backup",
			Envs: tasks.KV{
				"BACKUP_PATHS":  "tools:boot:init",
				"EXCLUDE_PATHS": "",
				"BACKUP_ROOT":   "/mnt/linux",
			},
			// restic backup -n tools boot init
			ExpectFiles: 6,
		},
		{
			Name: "path-exclude",
			Envs: tasks.KV{
				"BACKUP_PATHS":  "",
				"EXCLUDE_PATHS": "*.h:*.c:*.S:fs/nfs",
				"BACKUP_ROOT":   "/mnt/linux",
			},
			// restic backup -n --exclude *.h --exclude *.c --exclude *.S --exclude net/nfs .
			ExpectFiles: 54,
		},
		{
			Name: "path-backup-and-exclude",
			Envs: tasks.KV{
				"BACKUP_PATHS":  "kernel:net:fs",
				"EXCLUDE_PATHS": "*.h:*.c:*.S:fs/nfs",
				"BACKUP_ROOT":   "/mnt/linux",
			},
			// restic backup -n --exclude *.h --exclude *.c --exclude *.S --exclude fs/nfs kernel net fs
			ExpectFiles: 20,
		},
	}

	for _, testDataset := range testDatasets {

		testDataset := testDataset
		namespace := fmt.Sprintf("nginx-%v", testDataset.Name)
		config := tasks.KV{
			"bucket":    "mcs-backup-" + namespace,
			"namespace": namespace,
		}

		env := tasks.KV{
			"AWS_ACCESS_KEY_ID":     baseConfig["minioUser"],
			"AWS_SECRET_ACCESS_KEY": baseConfig["minioPassword"],
			"RESTIC_PASSWORD":       "some-secret-password",
			"RETENTION_POLICY":      "{last: 1}",
			"RESTIC_REPOSITORY":     "s3:http://minio.minio.svc:9000/" + config["bucket"] + "/test",
		}

		maps.Copy(env, testDataset.Envs)
		maps.Copy(config, baseConfig)

		prefix := "exec svc/nginx -c mcs-backup -- "
		tests = append(tests, buildParameterTestTask(
			testDataset.Name,
			fmt.Sprintf("path test %v (expect=%v)", testDataset.Name, testDataset.ExpectFiles),
			config,
			env, util.Steps{
				{
					Log:     "run backup",
					Kubectl: prefix + "mcs-backup backup",
				},
				{
					Log:         "test: backup: number of files",
					Kubectl:     prefix + "mcs-backup restic ls latest --long",
					FilterLines: `^-rw`,
					ExpectLines: testDataset.ExpectFiles,
				},
			},
		))
	}

	return tests
}
