package main

import (
	"fmt"

	"github.com/goyek/goyek/v2"
)

type Flow struct {
	Tasks map[string]*goyek.DefinedTask
	Flow  *goyek.Flow
}

func NewFlow() Flow {
	return Flow{
		Tasks: map[string]*goyek.DefinedTask{},
		Flow:  &goyek.Flow{},
	}
}

func (f Flow) Add(tasks ...goyek.Task) Flow {
	for _, task := range tasks {
		definedTask := f.Flow.Define(task)
		f.Tasks[definedTask.Name()] = definedTask
	}
	return f
}

func (f Flow) AddPipeline(name string, tasks ...string) Flow {
	pipeline := []*goyek.DefinedTask{}
	for _, task := range tasks {
		definedTask, found := f.Tasks[task]
		if !found {
			panic(fmt.Sprintf("task %q does not exist", task))
		}
		pipeline = append(pipeline, definedTask)
	}
	f.Add(goyek.Task{
		Name:  name,
		Usage: "pipeline",
		Deps:  pipeline,
	})
	return f
}
