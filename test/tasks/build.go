package tasks

import (
	_ "embed"

	"github.com/bitfield/script"
	"github.com/goyek/goyek/v2"
)

var pvcBackupImage = "mcs-backup-pvc"

func TaskBuildPvcBackup(dep *goyek.DefinedTask) goyek.Task {
	return goyek.Task{
		Name:  "build-pvc-backup-image",
		Usage: "build and push pvc backup image",
		Deps:  goyek.Deps{dep},
		Action: func(tf *goyek.A) {

			image := registry + "/" + pvcBackupImage + ":latest"

			tf.Log("build image")
			out, err := script.
				Exec("docker build -f docker/Dockerfile -t " + image + " .").String()
			if err != nil {
				tf.Errorf("docker build: %v", out)
			}

			tf.Log("push image")
			out, err = script.Exec("docker push " + image).String()
			if err != nil {
				tf.Errorf("docker push: %v", out)
			}
		},
	}
}
