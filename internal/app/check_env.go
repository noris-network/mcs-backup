package app

import (
	"log"
	"os"
)

func checkRequiredEnv() {

	missing := []string{}
	for _, name := range requiredEnv {
		value := os.Getenv(name)
		if len(value) == 0 {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		log.Fatalf("required environment variable(s) %v null or not set", missing)
	}
}
