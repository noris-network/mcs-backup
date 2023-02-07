package app

import (
	"log"
	"os"

	"github.com/iancoleman/strcase"
)

type pipeT struct {
	Script  string
	Enabled bool
	Phase   string
}

type pipesT struct {
	In  pipeT
	Out pipeT
}

func getPipes() pipesT {
	return pipesT{
		In:  getPipe("In"),
		Out: getPipe("Out"),
	}
}

func getPipe(name string) (pipe pipeT) {
	value := os.Getenv(strcase.ToScreamingSnake("Pipe" + name + "Script"))
	pipe.Phase = name
	if value == "" {
		return
	}
	if _, err := os.Stat(value); os.IsNotExist(err) {
		log.Printf("found %v pipe script name (%#v), but it does not exist\n", name, value)
		return
	}
	log.Printf("found %v pipe script (%v)\n", name, value)
	pipe.Enabled = true
	pipe.Script = value
	return pipe
}
