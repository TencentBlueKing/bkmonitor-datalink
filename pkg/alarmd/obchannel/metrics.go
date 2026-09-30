// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package obchannel

import (
	"context"
	"errors"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// A deployment without a metrics pipeline still has its counters: they are
// in the answering process's own registry. metrics.get reads them there by
// exact name, from the one namespace alarmd registers, so the CLI reads the
// numbers the page and the rules are written against without a scrape, a
// query service or a port-forward. It is the process's own counters and no
// more: a rate needs two reads, and another replica's counters need another
// read targeted at it. Counters only the Leader moves (source refresh,
// cutover) are read with control_leader, without first finding its name.

// MetricNamePattern is the names metrics.get accepts: alarmd's own families.
var MetricNamePattern = regexp.MustCompile(`^bkmonitor_alarmd_[a-z0-9_]+$`)

// MaxMetricNames bounds one read's names, MaxMetricSeries each family's
// series and MaxMetricLabelFilters the label filter.
const (
	MaxMetricNames        = 20
	MaxMetricSeries       = 500
	MaxMetricLabelFilters = 8
)

// MetricSeries is one series: its labels and its value, or for a histogram
// its cumulative buckets, count and sum.
type MetricSeries struct {
	Labels  map[string]string `json:"labels"`
	Value   *float64          `json:"value,omitempty"`
	Count   *uint64           `json:"count,omitempty"`
	Sum     *float64          `json:"sum,omitempty"`
	Buckets map[string]uint64 `json:"buckets,omitempty"`
	// NonFinite is the value when it is NaN or an infinity, which JSON
	// cannot carry as a number: a gauge at NaN is a fact, not no value.
	NonFinite string `json:"non_finite,omitempty"`
}

// MetricFamily is one family as the process holds it now.
type MetricFamily struct {
	Name      string         `json:"name"`
	Type      string         `json:"type"`
	Help      string         `json:"help,omitempty"`
	Series    []MetricSeries `json:"series"`
	Matched   int            `json:"matched"`
	Truncated bool           `json:"truncated,omitempty"`
}

// MetricsResult is one read. Absent names the families this process has
// not registered -- a name that is not there is said, not answered empty.
type MetricsResult struct {
	Families []MetricFamily `json:"families"`
	Absent   []string       `json:"absent,omitempty"`
	// GatherError is the registry's error when some collector failed and
	// the rest answered. A family that collector owns is then missing from
	// Families and listed under Absent, and Absent no longer proves the
	// family is not registered.
	GatherError string `json:"gather_error,omitempty"`
}

// MetricsOperations reads the answering process's registry.
func MetricsOperations(gatherer prometheus.Gatherer) []Operation {
	one := Field{Type: "string", Pattern: MetricNamePattern.String(), MinLength: 1, MaxLength: 200}
	filter := Field{Type: "string", Pattern: `^[a-zA-Z_][a-zA-Z0-9_]*=.{0,256}$`, MinLength: 2, MaxLength: 320}
	fields := map[string]Field{
		"names": {Type: "array", Items: &one, MaxItems: MaxMetricNames, UniqueItems: true,
			Description: "指标族全名，含 bkmonitor_alarmd_ 前缀，从 /metrics 或设计文档的注册表抄，不自行拼写。", Source: "注册表（pkg/alarmd/metric）"},
		"labels": {Type: "array", Items: &filter, MaxItems: MaxMetricLabelFilters, UniqueItems: true,
			Description: "可选：按标签精确匹配筛选序列，每项写成 name=value，只保留每项都相等的序列。"},
	}
	available := func() Availability {
		if gatherer == nil {
			return Availability{Reason: "metrics registry is not wired"}
		}
		return Availability{Available: true}
	}
	return []Operation{{
		ID:            "metrics.get",
		Summary:       "按名字读取应答进程自己的 alarmd 指标当前值（计数器、仪表、直方图）；速率要读两次相减，其他副本要指定实例再读，control_leader=true 直接读当前 Control Leader。",
		EvidenceScope: "process",
		Targetable:    true,
		Fields:        fields,
		Required:      []string{"names"},
		Limits:        map[string]any{"names": MaxMetricNames, "series_per_family": MaxMetricSeries, "label_filters": MaxMetricLabelFilters, "scope": "answering_replica"},
		OutputSchema:  SchemaOf(MetricsResult{}),
		Examples:      []Params{{"names": []any{"bkmonitor_alarmd_source_refresh_total"}}, {"names": []any{"bkmonitor_alarmd_source_refresh_total"}, "control_leader": true}},
		Availability:  available,
		Validate: func(p Params) error {
			names, _ := p["names"].([]any)
			if len(names) == 0 {
				return errors.New("names must name at least one metric family")
			}
			for _, raw := range names {
				name, _ := raw.(string)
				if !MetricNamePattern.MatchString(name) {
					return errors.New("names must be alarmd metric family names")
				}
			}
			return nil
		},
		Run: func(ctx context.Context, p Params) Outcome {
			if gatherer == nil {
				return Outcome{Error: &Failure{Code: "metrics_not_wired", Message: "This process has no metrics registry wired to the channel."}}
			}
			result, err := readMetrics(gatherer, metricNames(p), metricLabels(p))
			if err != nil {
				return Outcome{Error: &Failure{Code: "metrics_unreadable", Message: "The metrics registry could not be gathered."}}
			}
			out := Outcome{Value: result, Complete: len(result.Absent) == 0 && result.GatherError == "",
				Limitations: []string{"Values are this process's counters now; use meta.answered_by and read again to take a rate."}}
			if result.GatherError != "" {
				out.Limitations = append(out.Limitations, "Some collectors failed; absent families may be registered but unread: "+result.GatherError)
			}
			for _, family := range result.Families {
				if family.Truncated {
					out.Complete = false
					out.Limitations = append(out.Limitations, family.Name+" has more series than returned; narrow it with labels.")
				}
			}
			return out
		},
	}}
}

func metricNames(p Params) []string {
	raw, _ := p["names"].([]any)
	names := make([]string, 0, len(raw))
	for _, value := range raw {
		if name, ok := value.(string); ok {
			names = append(names, name)
		}
	}
	return names
}

func metricLabels(p Params) map[string]string {
	raw, _ := p["labels"].([]any)
	labels := make(map[string]string, len(raw))
	for _, value := range raw {
		text, _ := value.(string)
		if key, val, ok := strings.Cut(text, "="); ok {
			labels[key] = val
		}
	}
	return labels
}

// readMetrics gathers once and keeps the named families, each series
// matching every label filter, at most MaxMetricSeries a family.
func readMetrics(gatherer prometheus.Gatherer, names []string, labels map[string]string) (MetricsResult, error) {
	families, err := gatherer.Gather()
	if err != nil && len(families) == 0 {
		return MetricsResult{}, err
	}
	gatherError := ""
	if err != nil {
		gatherError = err.Error()
		if len(gatherError) > 512 {
			gatherError = gatherError[:512] + "..."
		}
	}
	byName := make(map[string]*dto.MetricFamily, len(families))
	for _, family := range families {
		byName[family.GetName()] = family
	}
	result := MetricsResult{Families: []MetricFamily{}, GatherError: gatherError}
	for _, name := range names {
		family, ok := byName[name]
		if !ok {
			result.Absent = append(result.Absent, name)
			continue
		}
		out := MetricFamily{Name: name, Type: family.GetType().String(), Help: family.GetHelp(), Series: []MetricSeries{}}
		matched := []*dto.Metric{}
		for _, metric := range family.GetMetric() {
			if labelsMatch(metricLabelMap(metric), labels) {
				matched = append(matched, metric)
			}
		}
		// Sorted before the cut, so two reads keep the same series and a
		// rate taken from them subtracts like from like.
		sort.Slice(matched, func(i, j int) bool {
			return labelKey(metricLabelMap(matched[i])) < labelKey(metricLabelMap(matched[j]))
		})
		out.Matched = len(matched)
		if len(matched) > MaxMetricSeries {
			matched, out.Truncated = matched[:MaxMetricSeries], true
		}
		for _, metric := range matched {
			series := MetricSeries{Labels: metricLabelMap(metric)}
			fillValue(&series, family.GetType(), metric)
			out.Series = append(out.Series, series)
		}
		result.Families = append(result.Families, out)
	}
	return result, nil
}

func metricLabelMap(metric *dto.Metric) map[string]string {
	labels := make(map[string]string, len(metric.GetLabel()))
	for _, pair := range metric.GetLabel() {
		labels[pair.GetName()] = pair.GetValue()
	}
	return labels
}

func labelsMatch(have, want map[string]string) bool {
	for key, value := range want {
		if have[key] != value {
			return false
		}
	}
	return true
}

func fillValue(series *MetricSeries, kind dto.MetricType, metric *dto.Metric) {
	set := func(v float64) {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			series.NonFinite = strconv.FormatFloat(v, 'g', -1, 64)
			return
		}
		series.Value = &v
	}
	switch kind {
	case dto.MetricType_COUNTER:
		set(metric.GetCounter().GetValue())
	case dto.MetricType_GAUGE:
		set(metric.GetGauge().GetValue())
	case dto.MetricType_UNTYPED:
		set(metric.GetUntyped().GetValue())
	case dto.MetricType_HISTOGRAM:
		histogram := metric.GetHistogram()
		count, sum := histogram.GetSampleCount(), histogram.GetSampleSum()
		series.Count, series.Sum = &count, &sum
		series.Buckets = map[string]uint64{"+Inf": count}
		for _, bucket := range histogram.GetBucket() {
			series.Buckets[strconv.FormatFloat(bucket.GetUpperBound(), 'g', -1, 64)] = bucket.GetCumulativeCount()
		}
	case dto.MetricType_SUMMARY:
		summary := metric.GetSummary()
		count, sum := summary.GetSampleCount(), summary.GetSampleSum()
		series.Count, series.Sum = &count, &sum
	}
}

func labelKey(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := ""
	for _, key := range keys {
		out += key + "=" + labels[key] + ","
	}
	return out
}
