package tests

import (
	"fmt"
	"int/tasks"
	"int/util"
	"strconv"

	"github.com/goyek/goyek/v2"
	"golang.org/x/exp/maps"
)

func TaskBuildRetentionTests(baseConfig tasks.KV) []goyek.Task {

	tests := []goyek.Task{}
	keepLast := 4

	for backupRuns := 2; backupRuns <= 6; backupRuns += 2 {

		namespace := fmt.Sprintf("nginx-retention-test-%v", backupRuns)
		config := tasks.KV{
			"bucket":    "mcs-backup-" + namespace,
			"namespace": namespace,
		}

		env := tasks.KV{
			"AWS_ACCESS_KEY_ID":     baseConfig["minioUser"],
			"AWS_SECRET_ACCESS_KEY": baseConfig["minioPassword"],
			"RESTIC_PASSWORD":       "some-secret-password",
			"RETENTION_POLICY":      fmt.Sprintf("{last: %v}", keepLast),
			"RESTIC_REPOSITORY":     "s3:http://minio.minio.svc:9000/" + config["bucket"] + "/test",
			"BACKUP_ROOT":           "/mnt",
		}

		maps.Copy(config, baseConfig)

		expect := keepLast
		if keepLast > backupRuns {
			expect = backupRuns
		}

		prefix := "exec svc/nginx -c mcs-backup -- "

		tests = append(tests, buildParameterTestTask(
			fmt.Sprintf("retention-%v", backupRuns),
			fmt.Sprintf("retention policy test (backups=%v,keepLast=%v)", backupRuns, keepLast),
			config,
			env, util.Steps{
				{
					Log:     fmt.Sprintf("do %v backups", backupRuns),
					Kubectl: prefix + "mcs-backup backup",
					Repeat:  backupRuns,
				},
				{
					Log:         fmt.Sprintf("test: retention policy: number of backups (expected=%v)", expect),
					Kubectl:     prefix + "mcs-backup snapshots",
					ExpectMatch: "^" + strconv.Itoa(expect) + " snapshots$",
				},
			},
		))
	}

	return tests
}
