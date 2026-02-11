package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/1set/cronrange"
	"github.com/afiskon/promtail-client/promtail"
	"github.com/joho/godotenv"
	m "github.com/noris-network/mcs-backup/internal/metrics"
	r "github.com/noris-network/mcs-backup/internal/restic"
	s "github.com/noris-network/mcs-backup/internal/s3"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v2"
)

type appT struct {
	Hooks hooksT
	Pipes pipesT
}

var app = appT{}
var backupEnabled bool
var basicAuth string
var cron *cronrange.CronRange
var crontabFile string
var crontabFileFound bool
var httpPort int
var metrics *m.Metrics
var restic *r.Restic
var s3 *s.Client
var updateCronChan chan *cronrange.CronRange
var nextHousekeepingRun time.Time
var housekeepingInterval = time.Duration(5*24) * time.Hour
var loki promtail.Client

func initEnv(verbose bool) {
	if initEnvRan {
		return
	}

	// try to load .env, ignore error
	err := godotenv.Load()
	if err == nil && verbose {
		log.Printf("loaded environment from '.env'")
	}

	// compose RESTIC_REPOSITORY env var
	if os.Getenv("RESTIC_REPOSITORY") == "" {
		repoBase := os.Getenv("RESTIC_REPOSITORY_BASE")
		repoPath := os.Getenv("RESTIC_REPOSITORY_PATH")
		if repoBase != "" && repoPath != "" {
			if verbose {
				log.Printf("set RESTIC_REPOSITORY from RESTIC_REPOSITORY_BASE and RESTIC_REPOSITORY_PATH")
			}
			os.Setenv("RESTIC_REPOSITORY", repoBase+"/"+strings.TrimPrefix(repoPath, "/"))
			os.Unsetenv("RESTIC_REPOSITORY_BASE")
			os.Unsetenv("RESTIC_REPOSITORY_PATH")
		}
	}

	{
		pruneIntervalStr, found := os.LookupEnv("BACKUP_PRUNE_INTERVAL")
		if found {
			if pruneInterval, err := time.ParseDuration(pruneIntervalStr); err != nil {
				log.Printf("warn: BACKUP_PRUNE_INTERVAL: %v", err)
			} else {
				if pruneInterval > 0 {
					housekeepingInterval = pruneInterval
					log.Printf("prune interval set to %v", pruneInterval)
				} else {
					log.Printf("auto prune disabled")
				}
			}
		}
	}

	checkRequiredEnv()

	initEnvRan = true
}

func initServer() {
	// setup api authentication
	password := os.Getenv("RESTIC_PASSWORD")
	passShaBytes := sha256.Sum224([]byte(password))
	passShaHex := hex.EncodeToString(passShaBytes[:])
	basicAuth = "Basic " + base64.StdEncoding.EncodeToString([]byte("backup:"+passShaHex))

	// configure http port
	port, err := strconv.Atoi(os.Getenv("BACKUP_HTTP_PORT"))
	if err != nil && os.Getenv("BACKUP_HTTP_PORT") != "" {
		log.Fatalf("BACKUP_HTTP_PORT: %v", err)
	}
	if port == 0 {
		port = 9000
	}
	httpPort = port

	backupEnabled = true
}

func initializeRestic(skipChecks bool) {
	// new restic wrapper
	restic = r.NewFromEnv(r.Opts{
		DryRun: viper.GetBool("dry-run"),
		Debug:  viper.GetBool("debug"),
	})
	if !skipChecks {
		log.Printf("check restic repository %#v", restic.Repository)
		if err := restic.Preflight(); err != nil {
			log.Fatalf("error: %s", err)
		}
		log.Printf("backup root: %v", restic.WorkDir)
	}

	if skipChecks {
		return
	}

	// hooks & pipes
	app.Hooks = getHooks()
	app.Pipes = getPipes()
}

func initializeS3(skipChecks bool) {
	// new s3 client, test connection
	s3 = s.NewFromEnv()
	if !skipChecks {
		log.Printf("check S3 credentials for endpoint %#v", s3.Endpoint)
		if err := s3.ConnectTest(); err != nil {
			log.Fatalf("error: %v", err)
		}
	}

	// MetricsTimeout
	s3timeout, err := time.ParseDuration(os.Getenv("S3_METRICS_TIMEOUT"))
	if err != nil && os.Getenv("S3_METRICS_TIMEOUT") != "" {
		log.Printf("S3_METRICS_TIMEOUT: %v", err)
	}
	if s3timeout == 0 {
		s3timeout = 5 * time.Second
	}
	s3.MetricsTimeout = s3timeout

	if skipChecks {
		return
	}

	// hooks & pipes
	app.Hooks = getHooks()
	app.Pipes = getPipes()
}

func initializeService(dryrun bool) {
	// make channels
	updateCronChan = make(chan *cronrange.CronRange, 1)

	// new metrics
	labels := labelsFromEnv()
	labels["repository_id"] = restic.RepositoryID
	metrics = m.NewFromEnv(m.Opts{
		PrometheusNamespace:       "mcsbackup",
		InfluxdbMeasurementPrefix: "mcsbackup_",
		Definition:                metricsDef,
		Providers:                 metricsProviders,
		Labels:                    labels,
		Debug:                     viper.GetBool("debug"),
	})

	if metrics.InfluxdbEnabled() {
		log.Printf("check influxdb %#v db=%#v", metrics.InfluxdbURL, metrics.InfluxdbDatabase)
		if err := metrics.InfluxdbCheck(); err != nil {
			log.Fatalf("error: %s", err)
		}
	}

	// configure loki
	lokiURL := os.Getenv("LOKI_URL")
	sendLevel := promtail.DISABLE
	if lokiURL != "" {
		lokiURL += "/api/prom/push"
		sendLevel = promtail.INFO
		log.Printf("will send logs to %#v", lokiURL)
	}
	labelsStr := "{"
	for k, v := range labels {
		labelsStr += fmt.Sprintf("%v=%#v,", k, v)
	}
	labelsStr = strings.TrimRight(labelsStr, ",") + "}"
	conf := promtail.ClientConfig{
		PushURL:            lokiURL,
		Labels:             labelsStr,
		BatchWait:          5 * time.Second,
		BatchEntriesNumber: 10000,
		SendLevel:          sendLevel,
		PrintLevel:         promtail.INFO,
	}
	loki, _ = promtail.NewClientJson(conf)

	// configure cron from file
	crontabFile = os.Getenv("CRON_SCHEDULE_FILE")
	if crontabFile != "" {
		if _, err := os.Stat(crontabFile); os.IsNotExist(err) {
			log.Printf("cron schedule file (%#v) not found: ignore", crontabFile)
		} else {
			cr, err := readCronScheduleFile(crontabFile)
			if err != nil {
				log.Fatalf("found cron schedule file %#v: %v", crontabFile, err)
			}
			crontabFileFound = true
			cron = cr
			go watchCronScheduleFile(updateCronChan)
			log.Printf("found cron schedule file %#v (%#v), will schedule backups accordingly, watcher started", crontabFile, cr.CronExpression())
		}
	}

	// configure cron from environment
	crontabSchedule := os.Getenv("CRON_SCHEDULE")
	if crontabSchedule != "" {
		if crontabFileFound {
			log.Fatalf("please set CRON_SCHEDULE *or* CRON_SCHEDULE_FILE, not both")
		}
		cr, err := cronrange.New(crontabSchedule, os.Getenv("TZ"), 1)
		if err != nil {
			log.Fatalf("cron schedule from environment invalid: %v", err)
		}
		cron = cr
		log.Printf(
			"found cron schedule(%#v) in environment, will schedule backups accordingly",
			crontabSchedule,
		)
	}

	// start event loop in background
	if cron != nil && !dryrun {
		go func() {
			for {

				nextOccurrency := cron.NextOccurrences(time.Now(), 1)[0]
				loki.Infof(
					"next run: %#v",
					nextOccurrency.Start.Format("2006-01-02 15:04:05 -0700 MST"),
				)

				select {

				case t := <-sleepUntil(nextOccurrency.Start):
					if t.IsZero() {
						loki.Infof("skip backup")
						continue
					}
					loki.Infof("backup triggered via cron")
					if err := fullBackupRun(); err != nil {
						loki.Errorf("cron: %v", err)
					}

				case c := <-updateCronChan:
					loki.Infof("update cron schedule...")
					cron = c

				}
			}
		}()
		log.Print("event loop started")
	} else {
		if !dryrun {
			log.Print("no cron schedule found, backups need to be triggered via api")
		}
	}

	// schedule next housekeeping run
	nextHousekeepingRun = time.Now().Add(housekeepingInterval)

	log.Printf("initialization and preflight checks done")
}

func sleepUntil(t time.Time) <-chan time.Time {
	tCh := make(chan time.Time)
	step := time.Duration(time.Minute)
	go func(t time.Time) {
		for {
			if t.Before(time.Now()) {
				tCh <- time.Time{}
				return
			}
			delta := time.Until(t)
			if delta > step {
				time.Sleep(step)
			} else {
				time.Sleep(delta)
				break
			}
		}
		tCh <- time.Now()
	}(t)
	return tCh
}

func readCronScheduleFile(file string) (cr *cronrange.CronRange, err error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return
	}
	cr, err = cronrange.New(string(data), os.Getenv("TZ"), 1)
	if err != nil {
		return cr, fmt.Errorf("cron schedule invalid, ignore (%v)", err)
	}
	return
}

func watchCronScheduleFile(notify chan *cronrange.CronRange) {
	lastmod := time.Time{}
	for {
		fileInfo, err := os.Stat(crontabFile)
		if fileInfo == nil {
			log.Print(err)
			time.Sleep(5 * time.Second)
			continue
		}
		modtime := fileInfo.ModTime()
		if lastmod.IsZero() {
			lastmod = modtime
		}
		if !modtime.After(lastmod) {
			time.Sleep(1 * time.Second)
			continue
		}
		cr, err := readCronScheduleFile(crontabFile)
		if err != nil {
			log.Printf("cron schedule invalid, ignore (%v)", err)
			time.Sleep(5 * time.Second)
			continue
		}
		lastmod = modtime
		notify <- cr
		time.Sleep(time.Second)
	}
}

func labelsFromEnv() m.Labels {
	labels := m.Labels{}

	log.Printf("read metric labels from environment:")

	if os.Getenv("METRICS_LABELS") == "" {
		log.Printf("no metrics labels found")
		return labels
	}

	// try to parse LABELS, treat values as interface so ints/floats/bools can also be handeled
	raw := map[string]any{}
	if err := yaml.Unmarshal([]byte(os.ExpandEnv(os.Getenv("METRICS_LABELS"))), &raw); err != nil {
		log.Printf("error: %v", err)
	}

	// try to convert all values to strings
	for k, v := range raw {
		labels[k] = fmt.Sprintf("%v", v)
		log.Printf("- %v: %v", k, v)
	}
	if len(labels) == 0 {
		log.Printf("no labels found")
	}
	return labels
}

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
