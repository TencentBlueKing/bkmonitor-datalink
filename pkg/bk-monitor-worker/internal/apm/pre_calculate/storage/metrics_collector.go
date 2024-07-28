package storage

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/prometheus/prompb"
)

// MetricsName
const (
	ApmServiceInstanceRelation = "apm_service_with_apm_service_instance_relation"
	ApmServiceK8sRelation      = "apm_service_instance_with_k8s_address_relation"
	ApmServiceSystemRelation   = "apm_service_instance_with_system_relation"

	ApmServiceFlow       = "apm_service_to_apm_service_flow"
	SystemApmServiceFlow = "system_to_apm_service_flow"
	ApmServiceSystemFlow = "apm_service_to_system_flow"
	SystemFlow           = "system_to_system_flow"
)

// Flow metrics category and kind
const (
	CategoryHttp       = "http"
	CategoryDb         = "db"
	CategoryMessaging  = "messaging"
	KindService        = "service"
	KindComponent      = "component"
	KindVirtualService = "virtualService"
	KindCustomService  = "remote_service"
)

type MetricCollector interface {
	Observe(value any)
	Collect() prompb.WriteRequest
	Ttl() time.Duration
}

func dimensionKeyToNameAndLabel(dimensionKey string, ignoreName bool) (string, []prompb.Label) {
	pairs := strings.Split(dimensionKey, ",")
	var labels []prompb.Label
	var name string
	for _, pair := range pairs {
		composition := strings.Split(pair, "=")
		if len(composition) == 2 {
			if composition[0] == "__name__" {
				name = composition[1]
				if !ignoreName {
					labels = append(labels, prompb.Label{Name: composition[0], Value: composition[1]})
				}
			} else {
				labels = append(labels, prompb.Label{Name: composition[0], Value: composition[1]})
			}
		}
	}
	return name, labels
}

type FlowMetricRecordStats struct {
	DurationValues []float64
}

type flowMetricStats struct {
	FlowDurationMax, FlowDurationMin, FlowDurationSum, FlowDurationCount float64
	FlowDurationBucket                                                   []float64
}

type flowMetricsCollector struct {
	mu  sync.Mutex
	ttl time.Duration

	data    map[string]*flowMetricStats
	buckets []float64
}

func (c *flowMetricsCollector) Ttl() time.Duration { return c.ttl }

func (c *flowMetricsCollector) Observe(value any) {
	c.mu.Lock()
	defer c.mu.Unlock()

	mapping := value.(map[string]*FlowMetricRecordStats)
	for dimensionKey, v := range mapping {
		for _, duration := range v.DurationValues {
			s, exist := c.data[dimensionKey]
			if !exist {
				s = &flowMetricStats{
					FlowDurationMax:    math.SmallestNonzeroFloat64,
					FlowDurationMin:    math.MaxFloat64,
					FlowDurationBucket: make([]float64, len(c.buckets)),
				}
			}

			s.FlowDurationCount++
			s.FlowDurationSum += duration

			if s.FlowDurationMax < duration {
				s.FlowDurationMax = duration
			}

			if s.FlowDurationMin > duration {
				s.FlowDurationMin = duration
			}

			for i := 0; i < len(c.buckets); i++ {
				if c.buckets[i] >= duration {
					s.FlowDurationBucket[i]++
				}
			}

			c.data[dimensionKey] = s
		}
	}
}

func (c *flowMetricsCollector) Collect() prompb.WriteRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	res := c.convert()
	c.data = make(map[string]*flowMetricStats)
	return res
}

func (c *flowMetricsCollector) convert() prompb.WriteRequest {

	copyLabels := func(labels []prompb.Label) []prompb.Label {
		newLabels := make([]prompb.Label, len(labels))
		copy(newLabels, labels)
		return newLabels
	}
	var series []prompb.TimeSeries
	var metricsName []string

	ts := time.Now().UnixNano() / int64(time.Millisecond)
	for key, stats := range c.data {
		name, labels := dimensionKeyToNameAndLabel(key, true)

		metricsName = append(metricsName, name)

		series = append(series, prompb.TimeSeries{
			Labels:  append(copyLabels(labels), prompb.Label{Name: "__name__", Value: fmt.Sprintf("%s%s", name, "_min")}),
			Samples: []prompb.Sample{{Value: stats.FlowDurationMin, Timestamp: ts}},
		})

		series = append(series, prompb.TimeSeries{
			Labels:  append(copyLabels(labels), prompb.Label{Name: "__name__", Value: fmt.Sprintf("%s%s", name, "_max")}),
			Samples: []prompb.Sample{{Value: stats.FlowDurationMax, Timestamp: ts}},
		})

		series = append(series, prompb.TimeSeries{
			Labels:  append(copyLabels(labels), prompb.Label{Name: "__name__", Value: fmt.Sprintf("%s%s", name, "_sum")}),
			Samples: []prompb.Sample{{Value: stats.FlowDurationSum, Timestamp: ts}},
		})

		series = append(series, prompb.TimeSeries{
			Labels:  append(copyLabels(labels), prompb.Label{Name: "__name__", Value: fmt.Sprintf("%s%s", name, "_count")}),
			Samples: []prompb.Sample{{Value: stats.FlowDurationCount, Timestamp: ts}},
		})

		for i := 0; i < len(stats.FlowDurationBucket); i++ {
			le := strconv.FormatFloat(c.buckets[i], 'f', -1, 64)
			if c.buckets[i] == math.MaxFloat64 {
				le = "+Inf"
			}
			series = append(series, prompb.TimeSeries{
				Labels: append(
					copyLabels(labels), []prompb.Label{
						{Name: "__name__", Value: name + "_bucket"},
						{Name: "le", Value: le},
					}...),
				Samples: []prompb.Sample{{Value: stats.FlowDurationBucket[i], Timestamp: ts}},
			})
		}
	}

	return prompb.WriteRequest{Timeseries: series}
}

func newFlowMetricCollector(buckets []float64, ttl time.Duration) *flowMetricsCollector {
	return &flowMetricsCollector{
		ttl:     ttl,
		data:    make(map[string]*flowMetricStats),
		buckets: buckets,
	}
}

type relationMetricsCollector struct {
	mu   sync.Mutex
	data map[string]time.Time
	ttl  time.Duration
}

func (r *relationMetricsCollector) Ttl() time.Duration { return r.ttl }

func (r *relationMetricsCollector) Observe(value any) {
	r.mu.Lock()
	defer r.mu.Unlock()

	labels := value.([]string)
	for _, s := range labels {
		if _, exist := r.data[s]; !exist {
			r.data[s] = time.Now()
		}
	}
}

func (r *relationMetricsCollector) Collect() prompb.WriteRequest {
	r.mu.Lock()
	defer r.mu.Unlock()

	edge := time.Now().Add(-r.ttl)
	var keys []string
	for dimensionKey, ts := range r.data {
		if ts.Before(edge) {
			logger.Debugf("[RelationMetricsCollector] key: %s expired, timestamp: %s", dimensionKey, ts)
			keys = append(keys, dimensionKey)
		}
	}
	res := r.convert(keys)
	for _, k := range keys {
		delete(r.data, k)
	}
	return res
}

func (r *relationMetricsCollector) convert(dimensionKeys []string) prompb.WriteRequest {

	var series []prompb.TimeSeries
	metricName := make(map[string]int, len(dimensionKeys))

	ts := time.Now().UnixNano() / int64(time.Millisecond)
	for _, key := range dimensionKeys {
		name, labels := dimensionKeyToNameAndLabel(key, false)
		series = append(series, prompb.TimeSeries{
			Labels:  labels,
			Samples: []prompb.Sample{{Value: 1, Timestamp: ts}},
		})
		metricName[name]++
	}

	return prompb.WriteRequest{Timeseries: series}
}

func newRelationMetricCollector(ttl time.Duration) *relationMetricsCollector {
	return &relationMetricsCollector{ttl: ttl, data: make(map[string]time.Time)}
}
