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
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
)

func metricsRegistry(t *testing.T) *prometheus.Registry {
	t.Helper()
	registry := prometheus.NewRegistry()
	rounds := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "bkmonitor_alarmd_source_refresh_total", Help: "rounds"}, []string{"status"})
	rounds.WithLabelValues("UNCHANGED").Add(1925)
	rounds.WithLabelValues("PUBLISHED").Add(168)
	size := prometheus.NewGauge(prometheus.GaugeOpts{Name: "bkmonitor_alarmd_redis_pool_size", Help: "size"})
	size.Set(64)
	latency := prometheus.NewHistogram(prometheus.HistogramOpts{Name: "bkmonitor_alarmd_loop_turn_duration_seconds", Help: "turns", Buckets: []float64{1, 10}})
	latency.Observe(0.5)
	latency.Observe(30)
	wide := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "bkmonitor_alarmd_wide_total", Help: "wide"}, []string{"k"})
	for i := 0; i < MaxMetricSeries+5; i++ {
		wide.WithLabelValues(strconv.Itoa(i)).Inc()
	}
	registry.MustRegister(rounds, size, latency, wide)
	return registry
}

func invokeMetrics(t *testing.T, c *Channel, params Params) (int, Response, MetricsResult) {
	t.Helper()
	status, out := call(t, c, envelope(c, "invoke", "metrics.get", params))
	var result MetricsResult
	if out.Result != nil {
		encoded, _ := json.Marshal(out.Result)
		_ = json.Unmarshal(encoded, &result)
	}
	return status, out, result
}

// The process's own counters, gauges and histograms by exact name: every
// series with its labels and value, a histogram with its buckets, and a
// name the process does not register said as absent, not answered empty.
func TestMetricsGetReadsTheProcesssOwnFamiliesByName(t *testing.T) {
	c := testChannel(t, &testAuth{}, MetricsOperations(metricsRegistry(t))...)
	status, out, result := invokeMetrics(t, c, Params{"names": []any{"bkmonitor_alarmd_source_refresh_total",
		"bkmonitor_alarmd_redis_pool_size", "bkmonitor_alarmd_loop_turn_duration_seconds", "bkmonitor_alarmd_not_registered_total"}})
	if status != 200 || out.Evidence.Complete || len(result.Absent) != 1 || result.Absent[0] != "bkmonitor_alarmd_not_registered_total" {
		t.Fatalf("status %d complete %v absent %v", status, out.Evidence.Complete, result.Absent)
	}
	if len(result.Families) != 3 {
		t.Fatalf("families = %+v", result.Families)
	}
	rounds := result.Families[0]
	if rounds.Type != "COUNTER" || len(rounds.Series) != 2 || rounds.Series[0].Labels["status"] != "PUBLISHED" || *rounds.Series[0].Value != 168 {
		t.Errorf("rounds = %+v", rounds)
	}
	if gauge := result.Families[1]; gauge.Type != "GAUGE" || *gauge.Series[0].Value != 64 {
		t.Errorf("gauge = %+v", gauge)
	}
	h := result.Families[2].Series[0]
	if *h.Count != 2 || h.Buckets["1"] != 1 || h.Buckets["10"] != 1 || h.Buckets["+Inf"] != 2 || *h.Sum != 30.5 {
		t.Errorf("histogram = %+v", h)
	}
}

// A label filter keeps only the series equal on every pair; a family with
// more series than the bound says it was cut and how many matched.
func TestMetricsGetFiltersByLabelAndSaysWhenItCut(t *testing.T) {
	c := testChannel(t, &testAuth{}, MetricsOperations(metricsRegistry(t))...)
	_, _, filtered := invokeMetrics(t, c, Params{"names": []any{"bkmonitor_alarmd_source_refresh_total"}, "labels": []any{"status=UNCHANGED"}})
	if s := filtered.Families[0].Series; len(s) != 1 || *s[0].Value != 1925 || filtered.Families[0].Matched != 1 {
		t.Errorf("filtered = %+v", filtered.Families[0])
	}
	_, out, wide := invokeMetrics(t, c, Params{"names": []any{"bkmonitor_alarmd_wide_total"}})
	if f := wide.Families[0]; !f.Truncated || len(f.Series) != MaxMetricSeries || f.Matched != MaxMetricSeries+5 || out.Evidence.Complete {
		t.Errorf("wide = truncated %v series %d matched %d complete %v", f.Truncated, len(f.Series), f.Matched, out.Evidence.Complete)
	}
}

// Only alarmd's own family names are accepted, and a channel without a
// registry lists the operation as unavailable with its reason.
func TestMetricsGetRefusesOtherNamesAndSaysWhenNotWired(t *testing.T) {
	c := testChannel(t, &testAuth{}, MetricsOperations(metricsRegistry(t))...)
	for _, names := range [][]any{{"go_goroutines"}, {"bkmonitor_alarmd_x", "process_open_fds"}, {}} {
		if status, _, _ := invokeMetrics(t, c, Params{"names": names}); status != 400 {
			t.Errorf("names %v: status %d, want refused", names, status)
		}
	}
	unwired := testChannel(t, &testAuth{}, MetricsOperations(nil)...)
	if status, out, _ := invokeMetrics(t, unwired, Params{"names": []any{"bkmonitor_alarmd_redis_pool_size"}}); status == 200 && out.Status == "ok" {
		t.Errorf("unwired answered ok: %+v", out)
	}
}

type failingCollector struct{ desc *prometheus.Desc }

func (c failingCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }
func (c failingCollector) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.NewInvalidMetric(c.desc, errFailingCollector)
}

var errFailingCollector = errors.New("collector read failed")

// A collector that fails does not make the read complete: its family is
// absent because it could not be read, not because it is not registered,
// and the answer says so with the registry's error. The other families are
// read as usual; a series of the registry sorted before the cut keeps two
// reads on the same series; a NaN gauge says NaN rather than nothing.
func TestMetricsGetSaysWhenACollectorFailedAndKeepsNonFiniteValues(t *testing.T) {
	registry := metricsRegistry(t)
	registry.MustRegister(failingCollector{desc: prometheus.NewDesc("bkmonitor_alarmd_broken_total", "broken", nil, nil)})
	nan := prometheus.NewGauge(prometheus.GaugeOpts{Name: "bkmonitor_alarmd_ratio", Help: "ratio"})
	nan.Set(math.NaN())
	registry.MustRegister(nan)
	c := testChannel(t, &testAuth{}, MetricsOperations(registry)...)
	status, out, result := invokeMetrics(t, c, Params{"names": []any{"bkmonitor_alarmd_broken_total", "bkmonitor_alarmd_redis_pool_size", "bkmonitor_alarmd_ratio"}})
	if status != 200 || out.Evidence.Complete || !strings.Contains(result.GatherError, "collector read failed") {
		t.Fatalf("status %d complete %v gather_error %q", status, out.Evidence.Complete, result.GatherError)
	}
	if len(result.Families) != 2 || *result.Families[0].Series[0].Value != 64 || result.Families[1].Series[0].NonFinite != "NaN" {
		t.Errorf("families = %+v", result.Families)
	}
	// The failed collector's family is registered and was not read: it is
	// unrecorded, and the read does not call it a counter that never moved.
	limits := strings.Join(out.Evidence.Limitations, "\n")
	if len(result.Unrecorded) != 1 || result.Unrecorded[0] != "bkmonitor_alarmd_broken_total" || len(result.Absent) != 0 ||
		!strings.Contains(limits, "were not read") || strings.Contains(limits, "has not moved") {
		t.Errorf("unrecorded %v absent %v limitations %q, want the unread family said to be unread", result.Unrecorded, result.Absent, limits)
	}
	// Asked only for families that answered, the read is still not
	// complete: which families the failed collector owns is not known.
	if _, out, only := invokeMetrics(t, c, Params{"names": []any{"bkmonitor_alarmd_redis_pool_size"}}); out.Evidence.Complete || len(only.Absent) != 0 || only.GatherError == "" {
		t.Errorf("read beside a failed collector: complete %v absent %v gather_error %q", out.Evidence.Complete, only.Absent, only.GatherError)
	}
	_, _, first := invokeMetrics(t, c, Params{"names": []any{"bkmonitor_alarmd_wide_total"}})
	_, _, second := invokeMetrics(t, c, Params{"names": []any{"bkmonitor_alarmd_wide_total"}})
	for i := range first.Families[0].Series {
		if first.Families[0].Series[i].Labels["k"] != second.Families[0].Series[i].Labels["k"] {
			t.Fatalf("two reads cut different series at %d", i)
		}
	}
}

// metrics.list names every alarmd family the process registers -- sorted,
// with its type, help and series count -- and only alarmd's; contains
// narrows it by name or help; a metrics.get that asked for a name the
// process does not register points at it.
func TestMetricsListNamesTheFamiliesSoNamesAreNotGuessed(t *testing.T) {
	registry := metricsRegistry(t)
	foreign := prometheus.NewGauge(prometheus.GaugeOpts{Name: "go_goroutines_fixture", Help: "not ours"})
	long := prometheus.NewCounter(prometheus.CounterOpts{Name: "bkmonitor_alarmd_long_help_total", Help: strings.Repeat("长", MaxListedHelpBytes)})
	registry.MustRegister(foreign, long)
	c := testChannel(t, &testAuth{}, MetricsOperations(registry)...)
	status, out := call(t, c, envelope(c, "invoke", "metrics.list", Params{}))
	if status != 200 || out.Status != "ok" {
		t.Fatalf("metrics.list = %d %+v", status, out)
	}
	encoded, _ := json.Marshal(out.Result)
	var result MetricsListResult
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, family := range result.Families {
		names = append(names, family.Name)
	}
	want := []string{"bkmonitor_alarmd_long_help_total", "bkmonitor_alarmd_loop_turn_duration_seconds", "bkmonitor_alarmd_redis_pool_size",
		"bkmonitor_alarmd_source_refresh_total", "bkmonitor_alarmd_wide_total"}
	if strings.Join(names, ",") != strings.Join(want, ",") || result.Matched != len(want) {
		t.Fatalf("listed %v (matched %d), want %v and nothing outside alarmd's namespace", names, result.Matched, want)
	}
	for _, family := range result.Families {
		switch family.Name {
		case "bkmonitor_alarmd_source_refresh_total":
			if family.Type != "COUNTER" || family.Help != "rounds" || family.Series != 2 {
				t.Fatalf("refresh family = %+v", family)
			}
		case "bkmonitor_alarmd_loop_turn_duration_seconds":
			if family.Type != "HISTOGRAM" || family.Series != 1 {
				t.Fatalf("histogram family = %+v", family)
			}
		case "bkmonitor_alarmd_long_help_total":
			if len(family.Help) > MaxListedHelpBytes+3 || !strings.HasSuffix(family.Help, "...") || !utf8.ValidString(family.Help) {
				t.Fatalf("long help not cut on a character boundary: %d bytes %q", len(family.Help), family.Help[len(family.Help)-8:])
			}
		}
	}
	_, narrowed := call(t, c, envelope(c, "invoke", "metrics.list", Params{"contains": "ROUNDS"}))
	encoded, _ = json.Marshal(narrowed.Result)
	var only MetricsListResult
	_ = json.Unmarshal(encoded, &only)
	if len(only.Families) != 1 || only.Families[0].Name != "bkmonitor_alarmd_source_refresh_total" {
		t.Fatalf("contains matched help case-insensitively? got %+v", only.Families)
	}
	_, got, _ := invokeMetrics(t, c, Params{"names": []any{"bkmonitor_alarmd_recovery_total"}})
	if len(got.Next) != 1 || got.Next[0].Operation != "metrics.list" {
		t.Fatalf("a name the process does not register gave no way to find the right one: %+v", got.Next)
	}
	_, known, _ := invokeMetrics(t, c, Params{"names": []any{"bkmonitor_alarmd_source_refresh_total"}})
	for _, next := range known.Next {
		if next.Operation == "metrics.list" {
			t.Fatal("a read of a registered family pointed at metrics.list")
		}
	}
	for _, bad := range []Params{{"contains": ""}, {"contains": "a;b"}, {"contains": strings.Repeat("a", 65)}} {
		if err := validate(c.ops["metrics.list"], bad); err == nil {
			t.Fatalf("contains %v accepted", bad)
		}
	}
}

// A registered family with no series yet -- a failure counter before its
// first failure -- is listed with no series and read as registered but not
// recorded: a zero, not a name the process does not have. Only a name the
// registry does not describe is absent and points at metrics.list.
func TestARegisteredFamilyWithoutSeriesIsUnrecordedNotAbsent(t *testing.T) {
	registry := metricsRegistry(t)
	failures := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "bkmonitor_alarmd_redis_command_failure_total", Help: "failed commands"}, []string{"reason"})
	registry.MustRegister(failures)
	c := testChannel(t, &testAuth{}, MetricsOperations(registry)...)
	_, out := call(t, c, envelope(c, "invoke", "metrics.list", Params{"contains": "failure"}))
	encoded, _ := json.Marshal(out.Result)
	var listed MetricsListResult
	_ = json.Unmarshal(encoded, &listed)
	if !listed.Described || len(listed.Families) != 1 || listed.Families[0].Name != "bkmonitor_alarmd_redis_command_failure_total" ||
		listed.Families[0].Series != 0 || listed.Families[0].Type != "" || listed.Families[0].Help != "failed commands" {
		t.Fatalf("an empty registered family is not listed as such: %+v", listed)
	}
	_, got, result := invokeMetrics(t, c, Params{"names": []any{"bkmonitor_alarmd_redis_command_failure_total", "bkmonitor_alarmd_not_registered_total"}})
	if len(result.Unrecorded) != 1 || result.Unrecorded[0] != "bkmonitor_alarmd_redis_command_failure_total" ||
		len(result.Absent) != 1 || result.Absent[0] != "bkmonitor_alarmd_not_registered_total" {
		t.Fatalf("unrecorded %v absent %v, want the empty family unrecorded and only the unknown name absent", result.Unrecorded, result.Absent)
	}
	_, onlyEmpty, _ := invokeMetrics(t, c, Params{"names": []any{"bkmonitor_alarmd_redis_command_failure_total"}})
	for _, next := range onlyEmpty.Next {
		if next.Operation == "metrics.list" {
			t.Fatal("a registered name without series was sent to look for its name")
		}
	}
	if len(got.Next) != 1 || got.Next[0].Operation != "metrics.list" {
		t.Fatalf("an unregistered name gave no way to find the right one: %+v", got.Next)
	}

	// A gatherer that cannot describe itself lists only what has series,
	// and says so.
	plain := prometheus.GathererFunc(registry.Gather)
	c = testChannel(t, &testAuth{}, MetricsOperations(plain)...)
	_, out = call(t, c, envelope(c, "invoke", "metrics.list", Params{}))
	encoded, _ = json.Marshal(out.Result)
	var partial MetricsListResult
	_ = json.Unmarshal(encoded, &partial)
	if partial.Described || out.Status != "partial" || !strings.Contains(strings.Join(out.Evidence.Limitations, "\n"), "only families that have series") {
		t.Fatalf("a listing without descriptions did not say what it leaves out: %+v %v", partial.Described, out.Evidence.Limitations)
	}
	for _, family := range partial.Families {
		if family.Name == "bkmonitor_alarmd_redis_command_failure_total" {
			t.Fatal("an undescribed listing claimed a family it cannot see")
		}
	}
}

// On the registry production builds, every family it describes is listed:
// read here without the listing's own parser, so a change in how
// client_golang prints a description fails this rather than quietly
// dropping the families that have no series yet.
func TestTheProductionRegistryIsListedWhole(t *testing.T) {
	gatherer := metric.NewRecorder(metric.BuildInfo{}).Gatherer()
	registry, ok := gatherer.(describer)
	if !ok {
		t.Fatal("the production gatherer cannot describe itself")
	}
	descs := make(chan *prometheus.Desc, 64)
	go func() { registry.Describe(descs); close(descs) }()
	want := map[string]bool{}
	for desc := range descs {
		text := desc.String()
		const marker = `fqName: "`
		start := strings.Index(text, marker)
		if start < 0 {
			t.Fatalf("a description without a name: %s", text)
		}
		name := text[start+len(marker):]
		name = name[:strings.IndexByte(name, '"')]
		if strings.HasPrefix(name, "bkmonitor_alarmd_") {
			want[name] = true
		}
	}
	listed, err := listMetrics(gatherer, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, family := range listed.Families {
		got[family.Name] = true
	}
	missing := []string{}
	for name := range want {
		if !got[name] {
			missing = append(missing, name)
		}
	}
	if len(want) == 0 || len(missing) > 0 || !listed.Described {
		t.Fatalf("described %d alarmd families, listing misses %d: %v", len(want), len(missing), missing)
	}
	if listed.Truncated {
		t.Fatalf("the production registry (%d families) no longer fits MaxListedFamilies (%d)", listed.Matched, MaxListedFamilies)
	}
}
