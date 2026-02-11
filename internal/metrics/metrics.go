package metrics

import (
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"reflect"
	"time"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	influxdb2api "github.com/influxdata/influxdb-client-go/v2/api"
	influxdb2dom "github.com/influxdata/influxdb-client-go/v2/domain"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const influxdbSendTimeout = 3 * time.Second

// Metrics struct
type Metrics struct {
	Debug                     bool
	Definition                Definition
	InfluxdbDatabase          string
	InfluxdbOrganization      string
	InfluxdbToken             string
	InfluxdbURL               string
	Labels                    Labels
	PrometheusNamespace       string
	Providers                 Providers
	gaugeVecs                 gaugeVecs
	influxdbClient            influxdb2.Client
	influxdbWriteAPI          influxdb2api.WriteAPIBlocking
	promLabelValues           []string
	promLabelNames            []string
	InfluxdbMeasurementPrefix string
}

// Labels type
type Labels map[string]string

// LabelNames type
type LabelNames []string

type gaugeVecs map[string]*prometheus.GaugeVec

// type influxMeasurements map[string]measurmentFields
type influxFields map[string]any

// Definition map
type Definition map[string]*struct {
	Name         string
	Help         string
	SkipInfluxdb bool
}

// Provider type
type Provider struct {
	Template            any
	PrometheusSubsystem string
	InfluxdbMeasurement string
	Labels              []LabelInit
}

// LabelInit type
type LabelInit struct {
	Name         string
	Values       []string
	InfluxdbOnly bool
}

// Providers type
type Providers []Provider

// Opts struct
type Opts struct {
	PrometheusNamespace       string
	InfluxdbMeasurementPrefix string
	Providers                 Providers
	Definition                Definition
	Labels                    Labels
	Debug                     bool
}

// Pair struct
type Pair struct {
	Datum       any
	LabelValues []string
	Publish     bool
}

// NoProvider type
type NoProvider string

// NewFromEnv func
func NewFromEnv(opts Opts) *Metrics {

	m := Metrics{
		InfluxdbMeasurementPrefix: opts.InfluxdbMeasurementPrefix,
		PrometheusNamespace:       opts.PrometheusNamespace,
		Providers:                 opts.Providers,
		Definition:                opts.Definition,
		Labels:                    opts.Labels,
		Debug:                     opts.Debug,
	}

	// enable influxdb support?
	m.InfluxdbURL = os.Getenv("INFLUXDB_URL")
	m.InfluxdbDatabase = os.Getenv("INFLUXDB_DATABASE")
	m.InfluxdbToken = os.Getenv("INFLUXDB_TOKEN")
	m.InfluxdbOrganization = os.Getenv("INFLUXDB_ORG")

	if m.InfluxdbURL != "" && m.InfluxdbDatabase != "" && m.InfluxdbToken != "" && m.InfluxdbOrganization != "" {
		log.Printf("influxdb support enabled")
		m.influxdbClient = influxdb2.NewClient(m.InfluxdbURL, m.InfluxdbToken)
		m.influxdbWriteAPI = m.influxdbClient.WriteAPIBlocking(
			m.InfluxdbOrganization,
			m.InfluxdbDatabase,
		)
	}
	return m.initialize()
}

// InfluxdbEnabled func
func (m Metrics) InfluxdbEnabled() bool {
	return m.influxdbClient != nil
}

// InfluxdbCheck func
func (m Metrics) InfluxdbCheck() error {
	checkTimeout := 2 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()
	ok, err := m.influxdbClient.Health(ctx)
	if err != nil {
		return fmt.Errorf("influxdb: %v", err)
	}
	if ok.Status != influxdb2dom.HealthCheckStatusPass {
		return errors.New("influxdb not ready")
	}

	// write test
	testPoint := influxdb2.NewPoint(
		m.InfluxdbMeasurementPrefix+"startup_write_test",
		map[string]string{},
		map[string]any{"test": 1},
		time.Now(),
	)
	ctx, cancel = context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()
	if err := m.influxdbWriteAPI.WritePoint(ctx, testPoint); err != nil {
		return fmt.Errorf("influxdb: %v", err)
	}
	return nil
}

// initialize func
func (m Metrics) initialize() *Metrics {
	// prepare labels
	for name, value := range m.Labels {
		m.promLabelNames = append(m.promLabelNames, name)
		m.promLabelValues = append(m.promLabelValues, value)
	}

	m.gaugeVecs = gaugeVecs{}

	// create prometheus object for every metric
	for defName, def := range m.Definition {
		foundIn := ""

		// search for metric in provivers
		for _, provider := range m.Providers {
			metrics := reflect.ValueOf(provider.Template)
			field := metrics.FieldByName(defName)
			if !field.IsValid() {
				continue
			}
			if foundIn != "" {
				log.Fatalf(
					"Metrics: found duplicate field %#v in %v and %T",
					defName,
					foundIn,
					provider.Template,
				)
			}

			// get prom metric name
			name := def.Name
			if name == "" {
				log.Fatalf(`Metrics: field %#v has no "Name"`, defName)
			}

			// get labels for metric, add provider specific labels
			labelNames := make([]string, len(m.promLabelNames))
			copy(labelNames, m.promLabelNames)
			for _, label := range provider.Labels {
				if !label.InfluxdbOnly {
					labelNames = append(labelNames, label.Name)
				}
			}

			// check if metric type is supported
			kind := field.Kind()
			if kind == reflect.Uint64 || kind == reflect.Float64 || kind == reflect.Bool {
				if m.Debug {
					log.Printf("register %#v with labels %#v\n", defName, labelNames)
				}
				m.gaugeVecs[defName] = promauto.NewGaugeVec(
					prometheus.GaugeOpts{
						Namespace: m.PrometheusNamespace,
						Subsystem: provider.PrometheusSubsystem,
						Name:      m.Definition[defName].Name,
						Help:      m.Definition[defName].Help,
					}, labelNames)
			}

			foundIn = fmt.Sprintf("%T", provider.Template)
		}
		if foundIn == "" {
			log.Fatalf("Metrics: field %#v not foundIn in providers", defName)
		}
	}

	return &m
}

// Apply updates metrics from registerd Provider types
func (m Metrics) Apply(pairs ...Pair) {
	for _, pair := range pairs {
		m.apply(pair)
	}
}

func (m Metrics) apply(pair Pair) {
	// skip apply for "NoProvider"
	if reflect.TypeOf(pair.Datum).String() == "metrics.NoProvider" {
		return
	}

	// lookup Provider
	var provider Provider
	for _, p := range m.Providers {
		if reflect.TypeOf(p.Template) == reflect.TypeOf(pair.Datum) {
			provider = p
		}
	}
	if provider.Template == nil {
		log.Printf("Metrics: %s not a registered data provider, skip", reflect.TypeOf(pair.Datum))
		return
	}

	fields := influxFields{}

	// get label values for metric, add provider specific label values
	labelValues := make([]string, len(m.promLabelValues))
	copy(labelValues, m.promLabelValues)
	for n, value := range pair.LabelValues {
		if !provider.Labels[n].InfluxdbOnly {
			labelValues = append(labelValues, value)
		}
	}

	for defName, def := range m.Definition {
		name := m.Definition[defName].Name
		field := reflect.ValueOf(pair.Datum).FieldByName(defName)
		if !field.IsValid() {
			continue
		}
		gauge := m.gaugeVecs[defName]
		switch field.Kind() {
		case reflect.Float64:
			gauge.WithLabelValues(labelValues...).Set(field.Interface().(float64))
			if !def.SkipInfluxdb {
				fields[name] = field.Interface().(float64)
			}
		case reflect.Uint64:
			gauge.WithLabelValues(labelValues...).Set(float64(field.Interface().(uint64)))
			// influxdb 1.8 has an issue with 2.0 forward compatibility for uint64: convert to int64;
			// cf. https://github.com/influxdata/influxdb/issues/17781
			if !def.SkipInfluxdb {
				fields[name] = int64(field.Interface().(uint64))
			}
		case reflect.Bool:
			ok := 0
			if field.Interface().(bool) {
				ok = 1
			}
			gauge.WithLabelValues(labelValues...).Set(float64(ok))
			if !def.SkipInfluxdb {
				fields[name] = ok
			}
		case reflect.String:
			// strings are not supported by prometheus
			fields[name] = field.Interface().(string)
		}
	}

	// send influxdb metrics, if enabled
	if m.InfluxdbEnabled() && pair.Publish {
		labels := Labels{}
		maps.Copy(labels, m.Labels)
		for n, v := range pair.LabelValues {
			labels[provider.Labels[n].Name] = v
		}

		point := influxdb2.NewPoint(
			m.InfluxdbMeasurementPrefix+provider.InfluxdbMeasurement,
			labels,
			fields,
			time.Now(),
		)
		ctx, cancel := context.WithTimeout(context.Background(), influxdbSendTimeout)
		defer cancel()
		if err := m.influxdbWriteAPI.WritePoint(ctx, point); err != nil {
			log.Printf("WritePoint: %v", err)
		}
	}
}
