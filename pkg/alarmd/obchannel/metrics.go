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
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

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

// A reader who does not know a family's exact name had nowhere to find it:
// metrics.get takes full names only, and an agent guessed one that does not
// exist. metrics.list names every family the process registers, with its
// type and help, so the name is copied rather than guessed.
//
// Registered is not the same as gathered. A gather drops a family with no
// series, and a failure counter has none until its first failure -- which
// is exactly when "has it ever happened" is the question. Both reads take
// the registered names from the registry's descriptions, so such a family
// is listed with no series and read as registered but never recorded,
// rather than as a name the process does not have.

// MetricNamePattern is the names metrics.get accepts: alarmd's own families.
var MetricNamePattern = regexp.MustCompile(`^bkmonitor_alarmd_[a-z0-9_]+$`)

// MaxMetricNames bounds one read's names, MaxMetricSeries each family's
// series and MaxMetricLabelFilters the label filter.
const (
	// MaxListedFamilies bounds metrics.list, and MaxListedHelpBytes each
	// family's help text in it.
	MaxListedFamilies  = 400
	MaxListedHelpBytes = 320

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

// MetricsResult is one read. Absent names the families this process does
// not register; Unrecorded the ones it registers that have no series yet --
// a counter that has not moved since the process started, which is a zero,
// not a missing name. When the registry cannot describe itself the two
// cannot be told apart and every missing family is Absent.
type MetricsResult struct {
	Families   []MetricFamily `json:"families"`
	Absent     []string       `json:"absent,omitempty"`
	Unrecorded []string       `json:"unrecorded,omitempty"`
	// GatherError is the registry's error when some collector failed and
	// the rest answered. A family that collector owns is then missing from
	// Families: under Unrecorded when the registry describes it, under
	// Absent when it cannot describe itself. Either way it was not read,
	// and Unrecorded no longer proves the counter has not moved.
	GatherError string `json:"gather_error,omitempty"`
}

// MetricFamilyEntry is one family metrics.list names: what metrics.get takes
// as its name, the family's type and help, and how many series it has now.
// A family with no series has no type to report: a description does not
// carry one, and the name is not a reliable guess.
type MetricFamilyEntry struct {
	Name   string `json:"name"`
	Type   string `json:"type,omitempty"`
	Help   string `json:"help,omitempty"`
	Series int    `json:"series"`
}

// MetricsListResult is the families the answering process registers, in
// name order. Truncated says more matched than MaxListedFamilies.
type MetricsListResult struct {
	// Described is false when the registry could not describe itself and
	// only families that have series are listed.
	Described   bool                `json:"described"`
	Families    []MetricFamilyEntry `json:"families"`
	Matched     int                 `json:"matched"`
	Truncated   bool                `json:"truncated,omitempty"`
	GatherError string              `json:"gather_error,omitempty"`
}

// MetricsOperations reads the answering process's registry.
func MetricsOperations(gatherer prometheus.Gatherer) []Operation {
	one := Field{Type: "string", Pattern: MetricNamePattern.String(), MinLength: 1, MaxLength: 200}
	filter := Field{Type: "string", Pattern: `^[a-zA-Z_][a-zA-Z0-9_]*=.{0,256}$`, MinLength: 2, MaxLength: 320}
	fields := map[string]Field{
		"names": {Type: "array", Items: &one, MaxItems: MaxMetricNames, UniqueItems: true,
			Description: "指标族全名，含 bkmonitor_alarmd_ 前缀，从 metrics.list 抄，不自行拼写。", Source: "metrics.list families[].name"},
		"labels": {Type: "array", Items: &filter, MaxItems: MaxMetricLabelFilters, UniqueItems: true,
			Description: "可选：按标签精确匹配筛选序列，每项写成 name=value，只保留每项都相等的序列。"},
	}
	available := func() Availability {
		if gatherer == nil {
			return Availability{Reason: "metrics registry is not wired"}
		}
		return Availability{Available: true}
	}
	list := Operation{
		ID:            "metrics.list",
		Summary:       "列出应答进程注册的 alarmd 指标族：名字、类型、说明和当前序列数；metrics.get 的名字从这里抄，不要猜。",
		EvidenceScope: "process",
		Targetable:    true,
		Fields: map[string]Field{
			"contains": {Type: "string", MinLength: 1, MaxLength: 64, Pattern: `^[A-Za-z0-9_ ]+$`,
				Description: "可选：只列名字或说明里含这段文字的族（不区分大小写），例如 recovery、cooldown。"},
		},
		OutputSchema: SchemaOf(MetricsListResult{}),
		Limits:       map[string]any{"families": MaxListedFamilies, "help_bytes": MaxListedHelpBytes, "scope": "answering_replica"},
		Examples:     []Params{{}, {"contains": "recovery"}},
		Availability: available,
		Run: func(ctx context.Context, p Params) Outcome {
			if gatherer == nil {
				return Outcome{Error: &Failure{Code: "metrics_not_wired", Message: "This process has no metrics registry wired to the channel."}}
			}
			result, err := listMetrics(gatherer, p.String("contains"))
			if err != nil {
				return Outcome{Error: &Failure{Code: "metrics_unreadable", Message: "The metrics registry could not be gathered."}}
			}
			out := Outcome{Value: result, Complete: !result.Truncated && result.GatherError == "",
				Summary:     fmt.Sprintf("应答进程注册了 %d 个匹配的 alarmd 指标族", result.Matched),
				Limitations: []string{"Families are this process's registry; another replica is read by targeting it. A family a failed collector owns is listed with no series; see gather_error."}}
			if result.Truncated {
				out.Limitations = append(out.Limitations, "More families match than are listed; narrow with contains.")
			}
			if !result.Described {
				out.Complete = false
				out.Limitations = append(out.Limitations, "The registry could not describe itself: only families that have series are listed, and a registered family with none is missing here.")
			}
			return out
		},
	}
	return []Operation{list, {
		ID:            "metrics.get",
		Summary:       "按名字读取应答进程自己的 alarmd 指标当前值（计数器、仪表、直方图）；名字用 metrics.list 查；速率要读两次相减，其他副本要指定实例再读，control_leader=true 直接读当前 Control Leader。",
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
			if len(result.Absent) > 0 {
				out.Next = append(out.Next, Call{Operation: "metrics.list", Params: Params{}, Reason: "有名字不在应答进程的注册表里；从这里查到确切的族名再读。"})
			}
			switch {
			case len(result.Unrecorded) > 0 && result.GatherError != "":
				out.Limitations = append(out.Limitations, "Unrecorded families are registered and were not read: a collector failed, so one may simply have gone unread rather than never moved; see gather_error.")
			case len(result.Unrecorded) > 0:
				out.Limitations = append(out.Limitations, "Unrecorded families are registered and have no series since this process started: a counter there has not moved.")
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

// listMetrics names the registered alarmd families, filtered by contains.
func listMetrics(gatherer prometheus.Gatherer, contains string) (MetricsListResult, error) {
	families, err := gatherer.Gather()
	if err != nil && len(families) == 0 {
		return MetricsListResult{}, err
	}
	result := MetricsListResult{Families: []MetricFamilyEntry{}}
	if err != nil {
		result.GatherError = err.Error()
		if len(result.GatherError) > 512 {
			result.GatherError = result.GatherError[:512] + "..."
		}
	}
	needle := strings.ToLower(contains)
	all := map[string]MetricFamilyEntry{}
	described, ok := describedFamilies(gatherer)
	result.Described = ok
	for name, help := range described {
		all[name] = MetricFamilyEntry{Name: name, Help: help}
	}
	for _, family := range families {
		all[family.GetName()] = MetricFamilyEntry{Name: family.GetName(), Type: family.GetType().String(), Help: family.GetHelp(), Series: len(family.GetMetric())}
	}
	entries := []MetricFamilyEntry{}
	for name, entry := range all {
		if !MetricNamePattern.MatchString(name) {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(name), needle) && !strings.Contains(strings.ToLower(entry.Help), needle) {
			continue
		}
		if len(entry.Help) > MaxListedHelpBytes {
			cut := MaxListedHelpBytes
			for cut > 0 && !utf8.RuneStart(entry.Help[cut]) {
				cut--
			}
			entry.Help = entry.Help[:cut] + "..."
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	result.Matched = len(entries)
	if len(entries) > MaxListedFamilies {
		entries, result.Truncated = entries[:MaxListedFamilies], true
	}
	result.Families = entries
	return result, nil
}

// describer is a gatherer that can also say what it registers, as a
// prometheus.Registry can.
type describer interface {
	Describe(chan<- *prometheus.Desc)
}

// descPattern reads the name and help out of a description. client_golang
// exposes neither field; its String form quotes both, and a description in
// another form is left out rather than guessed at.
var descPattern = regexp.MustCompile(`^Desc\{fqName: ("(?:[^"\\]|\\.)*"), help: ("(?:[^"\\]|\\.)*"),`)

// describedFamilies is every family the gatherer describes, name to help;
// false when it cannot describe itself.
func describedFamilies(gatherer prometheus.Gatherer) (map[string]string, bool) {
	registry, ok := gatherer.(describer)
	if !ok {
		return nil, false
	}
	descs := make(chan *prometheus.Desc, 64)
	go func() {
		registry.Describe(descs)
		close(descs)
	}()
	families := map[string]string{}
	for desc := range descs {
		match := descPattern.FindStringSubmatch(desc.String())
		if match == nil {
			continue
		}
		name, err := strconv.Unquote(match[1])
		if err != nil {
			continue
		}
		help, err := strconv.Unquote(match[2])
		if err != nil {
			continue
		}
		families[name] = help
	}
	return families, true
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
	described, _ := describedFamilies(gatherer)
	for _, name := range names {
		family, ok := byName[name]
		if !ok {
			if _, registered := described[name]; registered {
				result.Unrecorded = append(result.Unrecorded, name)
			} else {
				result.Absent = append(result.Absent, name)
			}
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
