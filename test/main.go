package main

import (
	"int/demo"
	"int/tasks"
	"int/tests"
	"os"
	"strings"
)

var config = tasks.KV{
	"minioUser":     "minio-user",
	"minioPassword": "minio-secret-123",
}

func main() {

	flow := NewFlow()

	// deployments
	for _, deploy := range []string{"minio", "monitoring-operator", "monitoring-config", "backup-monitoring", "data", "demo"} {
		for _, action := range []string{"create", "delete"} {
			flow.Add(tasks.TaskRunKustomize(action+"-"+deploy, action, "test/deploy/"+deploy))
		}
	}

	flow.
		Add(
			tasks.TaskCheckDependencies(),
			tasks.TaskCreateCluster(),
			tasks.TaskDeleteCluster(),
			tasks.TaskGetRegistryAddress(),
			tasks.TaskRunExternalCommand("import-images", "local/import-images.sh"),
			tasks.TaskWaitAllPodsReady(),
		).
		Add(
			tasks.TaskBuildPvcBackup(flow.Tasks["get-registry-address"]),
		).
		AddPipeline(
			"init",
			"check-dependencies",
			"create-cluster",
			"import-images",
			"build-pvc-backup-image",
			"create-data",
			"create-minio",
			"create-backup-monitoring",
			"create-monitoring-operator",
			"create-monitoring-config",
			"wait-all-pods-ready",
		).
		AddPipeline(
			"cleanup",
			"delete-cluster",
		).
		Add(tests.TaskBuildRetentionTests(config)...).
		Add(tests.TaskBuildRestoreTests(config)...).
		Add(tests.TaskBuildPathTests(config)...).
		Add(tests.TaskBuildPipeTests(config)...).
		Add(tests.TaskBuildDisableTests(config)...).
		Add(tests.TaskBuildPromMetricsTests(config)...).
		Add(tests.TaskBuildLokiLogsTests(config)...).
		Add(tests.TaskBuildInfluxMetricsTests(config)...).
		Add(tests.TaskBuildCronTests(config)...).
		Add(tests.TaskBuildUnlockTests(config)...).
		Add(tests.TaskBuildEnvTests(config)...)

		// wait for pods
	// for _, pod := range []string{"minio", "influxdb", "data", "loki"} {
	// 	flow.Add(tasks.TaskWaitPodReady("monitoring", "app=loki"),
	// 		tasks.TaskRunKustomize(action+"-"+pod, action, "test/deploy/"+pod))
	// }

	allTests := []string{}
	for _, task := range flow.Tasks {
		if strings.HasPrefix(task.Name(), "test-") && !strings.Contains(task.Usage(), "(slow)") {
			allTests = append(allTests, task.Name())
		}
	}

	flow.
		AddPipeline("all-tests",
			allTests...,
		).
		AddPipeline("all",
			"init", "all-tests", "cleanup",
		)

		/*
		 */

	flow.Add(demo.TaskDemo(config)...)

	flow.Flow.Use(ReportStatusWithRetry)

	flow.Flow.Main(os.Args[1:])
}
