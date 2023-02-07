package main

import (
	"github.com/noris-network/mcs-backup/internal/app"
)

var build = "dev"

func main() {
	app.Build = build
	app.Execute()
}
