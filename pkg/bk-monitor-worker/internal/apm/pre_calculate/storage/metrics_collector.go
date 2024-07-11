package storage

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/prometheus/prompb"
)

type MetricCollector interface {
	Observe(value any)
	Collect() []prompb.TimeSeries
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
	ts time.Time

	FlowDurationMax, FlowDurationMin, FlowDurationSum, FlowDurationCount float64
	FlowDurationBucket                                                   map[float64]int
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
			if _, exist := c.data[dimensionKey]; !exist {
				c.data[dimensionKey] = &flowMetricStats{
					ts:                 time.Now(),
					FlowDurationMax:    math.Inf(-1),
					FlowDurationMin:    math.Inf(1),
					FlowDurationBucket: make(map[float64]int),
				}
			}

			c.data[dimensionKey].FlowDurationCount++
			c.data[dimensionKey].FlowDurationSum += duration

			if duration > c.data[dimensionKey].FlowDurationMax {
				c.data[dimensionKey].FlowDurationMax = duration
			}

			if duration < c.data[dimensionKey].FlowDurationMin {
				c.data[dimensionKey].FlowDurationMin = duration
			}
			for _, bucket := range c.buckets {
				if duration <= bucket {
					c.data[dimensionKey].FlowDurationBucket[bucket]++
				}
			}
		}
	}
}

func (c *flowMetricsCollector) Collect() []prompb.TimeSeries {
	c.mu.Lock()
	defer c.mu.Unlock()

	edge := time.Now().Add(-c.ttl)
	var keys []string
	for dimensionKey, stats := range c.data {
		if stats.ts.Before(edge) {
			logger.Debugf("[FlowMetricsCollector] key: %s expired, timestamp: %s", dimensionKey, stats.ts)
			keys = append(keys, dimensionKey)
		}
	}

	res := c.convert(keys)
	for _, k := range keys {
		delete(c.data, k)
	}

	return res
}

func (c *flowMetricsCollector) convert(dimensionKeys []string) []prompb.TimeSeries {
	copyLabels := func(labels []prompb.Label) []prompb.Label {
		newLabels := make([]prompb.Label, len(labels))
		copy(newLabels, labels)
		return newLabels
	}
	var res []prompb.TimeSeries
	ts := time.Now().UnixNano() / int64(time.Millisecond)
	for _, key := range dimensionKeys {
		stats := c.data[key]
		logger.Debugf("[FlowMetricsCollector] key: %s expired, values: %+v", key, stats)
		name, labels := dimensionKeyToNameAndLabel(key, true)

		res = append(res, prompb.TimeSeries{
			Labels:  append(copyLabels(labels), prompb.Label{Name: "__name__", Value: fmt.Sprintf("%s%s", name, "_min")}),
			Samples: []prompb.Sample{{Value: stats.FlowDurationMin, Timestamp: ts}},
		})

		res = append(res, prompb.TimeSeries{
			Labels:  append(copyLabels(labels), prompb.Label{Name: "__name__", Value: fmt.Sprintf("%s%s", name, "_max")}),
			Samples: []prompb.Sample{{Value: stats.FlowDurationMax, Timestamp: ts}},
		})

		res = append(res, prompb.TimeSeries{
			Labels:  append(copyLabels(labels), prompb.Label{Name: "__name__", Value: fmt.Sprintf("%s%s", name, "_sum")}),
			Samples: []prompb.Sample{{Value: stats.FlowDurationSum, Timestamp: ts}},
		})

		res = append(res, prompb.TimeSeries{
			Labels:  append(copyLabels(labels), prompb.Label{Name: "__name__", Value: fmt.Sprintf("%s%s", name, "_count")}),
			Samples: []prompb.Sample{{Value: stats.FlowDurationCount, Timestamp: ts}},
		})

		for bucket, count := range stats.FlowDurationBucket {
			res = append(res, prompb.TimeSeries{
				Labels: append(
					copyLabels(labels), []prompb.Label{
						{Name: "__name__", Value: name + "_bucket"},
						{Name: "le", Value: fmt.Sprintf("%f", bucket)},
					}...),
				Samples: []prompb.Sample{{Value: float64(count), Timestamp: ts}},
			})
		}
	}

	return res
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

func (r *relationMetricsCollector) Collect() []prompb.TimeSeries {
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

func (r *relationMetricsCollector) convert(dimensionKeys []string) []prompb.TimeSeries {
	var res []prompb.TimeSeries
	ts := time.Now().UnixNano() / int64(time.Millisecond)
	for _, key := range dimensionKeys {
		_, labels := dimensionKeyToNameAndLabel(key, false)
		res = append(res, prompb.TimeSeries{
			Labels:  labels,
			Samples: []prompb.Sample{{Value: 1, Timestamp: ts}},
		})
	}

	return res
}

func newRelationMetricCollector(ttl time.Duration) *relationMetricsCollector {
	return &relationMetricsCollector{ttl: ttl, data: make(map[string]time.Time)}
}
