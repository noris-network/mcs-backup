package app

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"

	"github.com/iancoleman/strcase"
)

type hookT struct {
	Script  string
	Enabled bool
	Phase   string
}

func (h hookT) Run(args ...string) error {

	if !h.Enabled {
		log.Printf("no %#v hook found, skip", h.Phase)
		return nil
	}
	log.Printf("running %#v hook...", h.Phase)

	// prepare command execution
	cmd := exec.Command(h.Script, args...)
	stderr := bytes.Buffer{}
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("Hook: StdoutPipe: %w", err)
	}

	// start command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("Hook: Start: %w", err)
	}

	// read output
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		log.Printf("HOOK: %v", line)
	}

	// wait for the command to exit
	err = cmd.Wait()
	if err != nil {
		log.Printf("ERROR: %#v", stderr.String())
		return fmt.Errorf("Hook: Wait: %w", err)
	}

	log.Printf("%#v hook finished without errors", h.Phase)
	return nil
}

type hooksT struct {
	PreBackup   hookT
	PreRestore  hookT
	PostBackup  hookT
	PostRestore hookT
}

func getHooks() hooksT {
	return hooksT{
		PreBackup:   getHook("PreBackup"),
		PreRestore:  getHook("PreRestore"),
		PostBackup:  getHook("PostBackup"),
		PostRestore: getHook("PostRestore"),
	}
}

func getHook(name string) (hook hookT) {
	value := os.Getenv(strcase.ToScreamingSnake(name + "Script"))
	hook.Phase = name
	if value == "" {
		return
	}
	if _, err := os.Stat(value); os.IsNotExist(err) {
		log.Printf("found %v hook script name (%#v), but it does not exist\n", name, value)
		return
	}
	log.Printf("found %v hook script (%v)\n", name, value)
	hook.Enabled = true
	hook.Script = value
	return
}
