package app

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func init() {
	rootCmd.AddCommand(serveCmd)
	f := serveCmd.Flags()
	f.Bool("noauth", false, "disable api auth")
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "start backup server & serve metrics",
	Args:  cobra.NoArgs,
	Run:   serveFunc,
}

type status struct {
	Phase      uint64
	Duration   float64
	Successful uint64
	InProgress bool
	Error      string
}

type running struct {
	PhaseRunning uint64
}

type metadata struct {
	SnapshotLatest     uint64
	SnapshotsAvailable uint64
	SnapshotsForgot    uint64
}

var flush = func() {}
var cancelAutoEnable = make(chan struct{})

func serveFunc(cmd *cobra.Command, args []string) {

	viper.BindPFlags(cmd.Flags())

	PrintAppInfo()

	// initialize s3, restic & metrics
	initializeGeneral()
	initializeMain(false)
	initializeService()

	// create http handlers

	// prometheus
	http.Handle("/metrics", promhttp.Handler())

	// "homepage"
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="/metrics">goto metrics</a>`)
	})

	http.HandleFunc("/api/backup/", func(w http.ResponseWriter, req *http.Request) {
		warn := false
		msg := "backup is "
		switch {
		case !checkAuth(req):
			warn = true
			msg = "forbidden"
			w.WriteHeader(403)
		case req.URL.Path == "/api/backup/enable":
			msg += "now enabled"
			backupEnabled = true
		case req.URL.Path == "/api/backup/disable":
			msg += "now disabled"
			backupEnabled = false
		case req.URL.Path == "/api/backup/status":
			if backupEnabled {
				msg += "enabled"
			} else {
				msg += "disabled"
			}
		case strings.HasPrefix(req.URL.Path, "/api/backup/maintenance/"):
			sec, err := strconv.Atoi(req.URL.Path[len("/api/backup/maintenance/"):])
			switch {
			case err != nil:
				msg = err.Error()
			case sec == 0:
				msg = "duration == 0"
			default:
				backupEnabled = false
				duration := time.Duration(sec) * time.Second
				enableAt := time.Now().Add(duration).Truncate(time.Second)
				msg = fmt.Sprintf("backup disabled until %v", enableAt)
				select {
				case cancelAutoEnable <- struct{}{}:
					loki.Infof("cancel previous auto-enable")
				default:
				}
				go func() {
					select {
					case <-time.After(time.Until(enableAt)):
					case <-cancelAutoEnable:
						return
					}
					loki.Infof("backup enabled after maintenance window of %v", duration)
					backupEnabled = true
				}()
			}
		default:
			warn = true
			msg = fmt.Sprintf("unknown endpoint %#v called", req.URL.Path)
			w.WriteHeader(404)
		}
		fmt.Fprint(w, msg)
		msg = "api request: " + msg
		if warn {
			loki.Warnf(msg)
		} else {
			loki.Infof(msg)
		}
	})

	http.HandleFunc("/healthz", func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprint(w, "ok")
	})

	// backup
	http.HandleFunc("/api/backup", func(w http.ResponseWriter, req *http.Request) {

		if strings.ToUpper(req.Method) != "POST" {
			w.WriteHeader(400)
			return
		}

		if !checkAuth(req) {
			w.WriteHeader(403)
			fmt.Fprint(w, "forbidden")
			loki.Warnf("auth failed for api request")
			return
		}

		gid := getGID()
		log.Printf("[%d] backup triggered via API", gid)

		if err := runBackup(w); err != nil {
			loki.Errorf("[%d] error: %v", gid, err)
			fmt.Fprintf(w, "error: %#v", err)
			return
		}
	})

	backupEnabled = true

	// start http server
	log.Printf("serving metrics on port %v", httpPort)
	log.Printf("ready")
	log.Fatal(http.ListenAndServe(":"+strconv.Itoa(httpPort), nil))
}

func checkAuth(req *http.Request) bool {
	if viper.GetBool("noauth") {
		return true
	}
	return req.Header.Get("Authorization") == basicAuth
}

var lockCounter = uint64(0)
var mutex = &sync.Mutex{}

var backupLockingChan = make(chan struct{}, 1)

func runBackup(w http.ResponseWriter) error {

	loki.Infof("backup triggered via API")

	// write log output to STDOUT and ResponseWriter
	orig := log.Writer()
	multi := io.MultiWriter(orig, w)
	log.SetOutput(multi)
	defer log.SetOutput(orig)

	if flusher, ok := w.(http.Flusher); ok {
		flush = func() { flusher.Flush() }
		restic.FlushFunc = flush
		defer func() {
			flush = func() {}
			restic.FlushFunc = flush
		}()
	}
	defer func() {
		flush = func() {}
		restic.FlushFunc = nil
	}()

	return fullBackupRun()
}

func getGID() uint64 {
	b := make([]byte, 64)
	b = b[:runtime.Stack(b, false)]
	b = bytes.TrimPrefix(b, []byte("goroutine "))
	b = b[:bytes.IndexByte(b, ' ')]
	n, _ := strconv.ParseUint(string(b), 10, 64)
	return n
}
