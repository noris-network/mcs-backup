package files

import (
	_ "embed"
)

//go:embed deployment-nginx.yaml
var NginxYaml string

//go:embed configmap-scripts.yaml
var ScriptsYaml string

//go:embed configmap-cron.yaml
var CronYaml string
