package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/goyek/goyek/v2"
)

var failed = []string{}
var retryFailed = os.Getenv("RETRY") == "true"

// ReportStatusWithRetry
func ReportStatusWithRetry(next goyek.Runner) goyek.Runner {
	return func(in goyek.Input) goyek.Result {

		fmt.Fprintf(in.Output, "===== TASK  %s\n", in.TaskName)
		start := time.Now()

		retry := retryFailed

		var res goyek.Result
		for {

			res = next(in)

			fmt.Fprintf(in.Output, "----- %s: %s (%.2fs)\n", res.Status.String(), in.TaskName, time.Since(start).Seconds())
			if res.Status == goyek.StatusFailed && retry {
				fmt.Fprintf(in.Output, "----- retry %s...\n", in.TaskName)
				failed = append(failed, in.TaskName)
				retry = false
				continue
			}
			break
		}

		if res.PanicStack != nil {
			if res.PanicValue != nil {
				io.WriteString(in.Output, fmt.Sprintf("panic: %v", res.PanicValue))
			} else {
				io.WriteString(in.Output, "panic(nil) or runtime.Goexit() called")
			}
			io.WriteString(in.Output, "\n\n")
			in.Output.Write(res.PanicStack)
		}

		return res
	}
}
