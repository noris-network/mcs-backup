package util

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/bitfield/script"
	"github.com/goyek/goyek/v2"
	"github.com/logrusorgru/aurora"
)

type TestFunc func() error

type Step struct {
	Log         string
	Func        TestFunc
	Kubectl     string
	IgnoreError bool
	UntilOK     bool
	Repeat      int
	ExpectLines int
	FilterLines string
	ExpectMatch string
	Hint        string
	Sleep       time.Duration
	Args        Args
}

type Steps []Step
type Args []any

var debug = os.Getenv("MCS_BACKUP_DEBUG") == "true"

func PrintDebug(info, str string) {
	fmt.Println(aurora.Gray(12, info+" "+strings.Repeat(">", 65)))
	fmt.Println(aurora.Blue(strings.TrimRight(str, "\n")))
	fmt.Println(aurora.Gray(12, "END "+strings.Repeat(">", 68)))
}

func RunSteps(tf *goyek.A, namespace string, steps ...Step) error {
	for _, step := range steps {
		if step.Log != "" {
			tf.Log(step.Log)
		}
		if step.Log == "" && step.Kubectl != "" {
			tf.Log(step.Kubectl)
		}
		if step.Sleep > 0 {
			time.Sleep(step.Sleep)
			continue
		}
		n := 0
		for {
			n++
			if step.Repeat > 0 {
				tf.Logf("#%v", n)
			}
			if step.Func != nil {
				if err := step.Func(); err != nil {
					return err
				}
			}
			if step.Kubectl != "" {
				kubectl := fmt.Sprintf(step.Kubectl, step.Args...)
				if strings.Contains(kubectl, "%!") {
					return fmt.Errorf("format error: %v", kubectl)
				}
				hint := ""
				if step.Hint != "" {
					hint = fmt.Sprintf(" (hint: %v)", step.Hint)
				}
				for {
					nNamespace := "-n " + namespace + " "
					if strings.HasPrefix(kubectl, "delete namespace ") {
						nNamespace = ""
					}
					retry := true
					var out string
					var err error
					for {
						out, err = script.Exec("kubectl " + nNamespace + kubectl).String()
						if err != nil && retry && strings.Contains(err.Error(), "Error from server:") {
							tf.Logf("retry kubectl command...")
							time.Sleep(100 * time.Millisecond)
							retry = false
							continue
						}
						break
					}
					if debug {
						PrintDebug("OUTPUT", out)
					}
					if !step.IgnoreError && !step.UntilOK && err != nil {
						return fmt.Errorf("script: %v%v", out, hint)
					}
					if step.ExpectMatch != "" {
						filter := regexp.MustCompile(step.ExpectMatch)
						lines, err := script.Echo(out).MatchRegexp(filter).CountLines()
						if err != nil {
							return fmt.Errorf("script: %v", out)
						}
						if lines == 0 {
							return fmt.Errorf("line %q not found%v", step.ExpectMatch, hint)
						}
					}
					if step.ExpectLines != 0 && !step.UntilOK {
						expect := step.ExpectLines
						if expect < 0 {
							expect = 0
						}
						filter := regexp.MustCompile(`.*`)
						if step.FilterLines != "" {
							filter = regexp.MustCompile(step.FilterLines)
						}
						lines, err := script.Echo(out).MatchRegexp(filter).CountLines()
						if err != nil {
							return fmt.Errorf("script: %v", out)
						}
						if lines == expect {
							tf.Logf("line count: %v", expect)
						} else {
							return fmt.Errorf("line %q count: got %v, expected: %v%v", step.FilterLines, lines, expect, hint)
						}
					}
					if err == nil {
						break
					}
					if err != nil && step.UntilOK {
						tf.Log("wait...")
						time.Sleep(time.Second)
						continue
					}
					break
				}
			}
			if n < step.Repeat {
				continue
			}
			break
		}
	}
	return nil
}
