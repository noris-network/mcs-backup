package app

import (
	m "github.com/noris-network/mcs-backup/internal/metrics"
	r "github.com/noris-network/mcs-backup/internal/restic"
	s "github.com/noris-network/mcs-backup/internal/s3"
)

var applicationID string
var applicationLabel string

// METRIC AND LABEL NAMING, see
// https://prometheus.io/docs/practices/naming/

var metricsDef = m.Definition{

	// TODO: check & fix "Help" texts

	// restic.Summary...
	"DataProcessed": {
		Help: "Number bytes processed",
		Name: "data_processed_bytes",
	},
	"DataAdded": {
		Help: "Number of added bytes added",
		Name: "data_added_bytes",
	},
	"DataBlobs": {
		Help: "Total number of blobs",
		Name: "data_blobs_total",
	},
	"DirsChanged": {
		Help: "Number of changed directories",
		Name: "dirs_changed_total",
	},
	"DirsNew": {
		Help: "Number of new directories",
		Name: "dirs_new_total",
	},
	"DirsUnmodified": {
		Help: "Number of unmodified directories",
		Name: "dirs_unmodified_total",
	},
	"FilesChanged": {
		Help: "Number of changed files",
		Name: "files_changed_total",
	},
	"FilesNew": {
		Help: "Number of new files",
		Name: "files_new_total",
	},
	"FilesProcessed": {
		Help: "Number of processed files",
		Name: "files_processed_total",
	},
	"FilesUnmodified": {
		Help: "Number of unmodified files",
		Name: "files_unmodified_total",
	},
	"TreeBlobs": {
		Help: "Number of tree blobs",
		Name: "tree_blobs_total",
	},

	// app.status
	"InProgress": {
		Help:         "Phase running now",
		Name:         "inprogress_id",
		SkipInfluxdb: true,
	},
	"Successful": {
		Help: "Phase status",
		Name: "success",
	},
	"Duration": {
		Help: "Phase Duration",
		Name: "duration_seconds",
	},
	"Error": {
		Help: "Error message of failed backup",
		Name: "error",
	},

	// app.running
	"PhaseRunning": {
		Help: "Phase running, 0=none",
		Name: "phase_running",
	},

	// app.metadata
	"SnapshotLatest": {
		Help: "Number of expired snapshots",
		Name: "snapshot_latest_unixtime",
	},
	"SnapshotsAvailable": {
		Help: "Number of expired snapshots",
		Name: "snapshots_available_total",
	},
	"SnapshotsForgot": {
		Help: "Number of expired snapshots",
		Name: "snapshots_forgot_total",
	},

	// s3.Metrics
	"BucketSize": {
		Help: "Number of incomplete uploads",
		Name: "bucket_size_bytes",
	},
	"Objects": {
		Help: "Number of S3 Objects",
		Name: "objects_total",
	},
	"StatsDuration": {
		Help: "Duration of collecting S3 stats",
		Name: "stats_duration_seconds",
	},
	"StatsTimeout": {
		Help: "Configured timeout for collecting S3 stats",
		Name: "stats_timeout_seconds",
	},
}

var metricsProviders = m.Providers{
	{
		Template:            r.Summary{},
		PrometheusSubsystem: "restic",
		InfluxdbMeasurement: "restic",
	},
	{
		Template:            s.Metrics{},
		PrometheusSubsystem: "s3",
		InfluxdbMeasurement: "s3",
		Labels: []m.LabelInit{
			{
				Name: "bucket",
			},
		},
	},
	{
		Template:            metadata{},
		PrometheusSubsystem: "",
		InfluxdbMeasurement: "meta",
	},
	{
		Template:            running{},
		PrometheusSubsystem: "",
		InfluxdbMeasurement: "running",
	},
	{
		Template:            status{},
		PrometheusSubsystem: "status",
		InfluxdbMeasurement: "status",
		Labels: []m.LabelInit{
			{
				Name:   "phase",
				Values: []string{"backup", "forget", "prune", "getstats", "s3stats", "overall"},
			},
			{
				Name:         "successful",
				Values:       []string{"true", "false"},
				InfluxdbOnly: true,
			},
		},
	},
}
