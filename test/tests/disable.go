package tests

import (
	"fmt"
	"int/tasks"
	"int/util"
	"time"

	"github.com/goyek/goyek/v2"
	"golang.org/x/exp/maps"
)

type DisableTest struct {
	Name  string
	Steps util.Steps
}

func TaskBuildDisableTests(baseConfig tasks.KV) []goyek.Task {

	tests := []goyek.Task{}

	prefix := "exec svc/nginx -c mcs-backup -- "
	testDatasets := []DisableTest{
		{
			Name: "disable",
			Steps: []util.Step{
				{
					Log:     "mcs-backup: create first backup",
					Kubectl: prefix + "mcs-backup backup",
				},
				{
					Log:         "check number of snapshots (1 expected)",
					Kubectl:     prefix + "restic snapshots",
					ExpectMatch: "^1 snapshots$",
				},
				{
					Log:         "disable backup",
					Kubectl:     prefix + "mcs-backup backup --disable",
					ExpectMatch: "^backup is now disabled$",
				},
				{
					Log:         "check mcs-backup logs",
					Kubectl:     "logs svc/nginx -c mcs-backup --tail=1",
					ExpectMatch: "api request: backup is now disabled",
				},
				{
					Log:     "mcs-backup: try to create three more backups (should fail because backup is disabled)",
					Kubectl: prefix + "mcs-backup backup",
					Repeat:  3,
				},
				{
					Log:         "check mcs-backup logs",
					Kubectl:     "logs svc/nginx -c mcs-backup --tail=2",
					ExpectMatch: "backup triggered via API",
				},
				{
					Log:         "check mcs-backup logs",
					Kubectl:     "logs svc/nginx -c mcs-backup --tail=2",
					ExpectMatch: "backup is disabled",
				},
				{
					Log:         "enable backup",
					Kubectl:     prefix + "mcs-backup backup --enable",
					ExpectMatch: "^backup is now enabled$",
				},
				{
					Log:         "check mcs-backup logs",
					Kubectl:     "logs svc/nginx -c mcs-backup --tail=1",
					ExpectMatch: "api request: backup is now enabled",
				},
				{
					Log:     "mcs-backup: create second backup",
					Kubectl: prefix + "mcs-backup backup",
				},
				{
					Log:         "check number of snapshots (2 expected)",
					Kubectl:     prefix + "restic snapshots",
					ExpectMatch: "^2 snapshots$",
				},
			},
		},
		{
			Name: "maintenance",
			Steps: []util.Step{
				{
					Log:     "mcs-backup: create first backup",
					Kubectl: prefix + "mcs-backup backup",
				},
				{
					Log:         "check number of snapshots (1 expected)",
					Kubectl:     prefix + "restic snapshots",
					ExpectMatch: "^1 snapshots$",
				},
				{
					Log:         "disable backup for 2s (maintenance)",
					Kubectl:     prefix + "mcs-backup backup --maintenance 2s",
					ExpectMatch: "^backup disabled until",
				},
				{
					Log:         "check mcs-backup logs",
					Kubectl:     "logs svc/nginx -c mcs-backup --tail=1",
					ExpectMatch: "api request: backup disabled until",
				},
				{
					Log:     "mcs-backup: try to create backup (should fail because of maintenance)",
					Kubectl: prefix + "mcs-backup backup",
				},
				{
					Log:         "check mcs-backup logs",
					Kubectl:     "logs svc/nginx -c mcs-backup --tail=1",
					ExpectMatch: "backup is disabled",
				},
				{
					Log:   "wait for end of maintenance window",
					Sleep: 2 * time.Second,
				},
				{
					Log:         "mcs-backup: create second backup",
					Kubectl:     prefix + "mcs-backup backup",
					ExpectMatch: "backup triggered via API",
				},
				{
					Log:   "wait for backup to finish",
					Sleep: 2 * time.Second,
				},
				{
					Log:         "check mcs-backup logs",
					Kubectl:     "logs svc/nginx -c mcs-backup --tail=1",
					ExpectMatch: "backup finished",
				},
				{
					Log:         "check number of snapshots (2 expected)",
					Kubectl:     prefix + "restic snapshots",
					ExpectMatch: "^2 snapshots$",
				},
			},
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
			"RETENTION_POLICY":      "{last: 10}",
			"RESTIC_REPOSITORY":     "s3:http://minio.minio.svc:9000/" + config["bucket"] + "/test",
			"BACKUP_ROOT":           "/mnt/linux",
		}

		maps.Copy(config, baseConfig)

		tests = append(tests, buildParameterTestTask(
			testDataset.Name,
			fmt.Sprintf("test %v", testDataset.Name),
			config, env, testDataset.Steps,
		))
	}

	return tests
}
