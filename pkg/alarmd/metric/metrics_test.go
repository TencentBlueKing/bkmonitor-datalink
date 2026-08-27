// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"context"
	"math"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lifecycle"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestRecorderUsesPrivateRegistries(t *testing.T) {
	first := NewRecorder(BuildInfo{Version: "v1", Commit: "a", SchemaVersion: "none"})
	second := NewRecorder(BuildInfo{Version: "v2", Commit: "b", SchemaVersion: "none"})

	if first.Gatherer() == second.Gatherer() {
		t.Fatal("recorders unexpectedly share a registry")
	}
	if got := scrape(t, first); !strings.Contains(got, `version="v1"`) || strings.Contains(got, `version="v2"`) {
		t.Fatalf("first registry contains unexpected build labels:\n%s", got)
	}
	if got := scrape(t, second); !strings.Contains(got, `version="v2"`) || strings.Contains(got, `version="v1"`) {
		t.Fatalf("second registry contains unexpected build labels:\n%s", got)
	}
}

func TestUnknownLabelValuesCollapseToOther(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	sensitive := "strategy-123 invalid URL https://internal.invalid"

	recorder.RecordProcess(Stage(sensitive), Mode(sensitive), Status(sensitive), ErrorCode(sensitive), time.Second)
	recorder.RecordRecords(Stage(sensitive), Mode(sensitive), Direction(sensitive), RecordType(sensitive), 1)
	recorder.RecordPipelineLatency(Stage(sensitive), Stage(sensitive), Mode(sensitive), time.Second)
	recorder.RecordShadowCompare(Component(sensitive), CompareResult(sensitive))
	recorder.Observe(context.Background(), observability.Observation{
		Component: observability.Component(sensitive),
		Stage:     observability.Stage(sensitive),
		Result:    observability.Result(sensitive),
		ReasonCode: observability.ReasonCode(
			sensitive,
		),
	})

	got := scrape(t, recorder)
	if strings.Contains(got, sensitive) || strings.Contains(got, "strategy-123") || strings.Contains(got, "internal.invalid") {
		t.Fatalf("dynamic sensitive value leaked into metrics:\n%s", got)
	}
	if !strings.Contains(got, `_other`) {
		t.Fatalf("unknown labels were not collapsed to _other:\n%s", got)
	}
}

func TestRecordRecordsRejectsNonFiniteCounts(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	recorder.RecordRecords(StageTrigger, ModeShadow, DirectionInput, RecordDetectionOutcome, math.NaN())
	recorder.RecordRecords(StageTrigger, ModeShadow, DirectionInput, RecordDetectionOutcome, math.Inf(1))

	if got := scrape(t, recorder); strings.Contains(got, "bkmonitor_alarmd_records_total") {
		t.Fatalf("non-finite record count created a time series:\n%s", got)
	}
}

func TestObservationRejectsNegativeDurationAndCounts(t *testing.T) {
	t.Parallel()

	recorder := NewRecorder(BuildInfo{})
	recorder.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentDetect,
		Stage:     observability.StageDetectCompleted,
		Result:    observability.ResultSuccess,
		Direction: observability.DirectionInternal,
		Duration:  -time.Second,
		Counts:    observability.Counts{Messages: -1},
	})
	got := scrape(t, recorder)
	if strings.Contains(got, "bkmonitor_alarmd_observation_duration_seconds") {
		t.Fatalf("negative duration created a histogram:\n%s", got)
	}
	if strings.Contains(got, "bkmonitor_alarmd_observed_messages_total") {
		t.Fatalf("negative count created a counter:\n%s", got)
	}
}

func TestKnownM0ReasonMapsToBoundedMetricValue(t *testing.T) {
	t.Parallel()

	recorder := NewRecorder(BuildInfo{})
	recorder.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentAdapter, Stage: observability.StageRejected,
		Result: observability.ResultTerminal, Direction: observability.DirectionInternal,
		ReasonCode: observability.ReasonCode(contract.ReasonRecordIdentityConflict),
	})
	got := scrape(t, recorder)
	if !strings.Contains(got, `reason_code="contract_deterministic"`) {
		t.Fatalf("known M0 reason was not mapped to the bounded metric value:\n%s", got)
	}
	if strings.Contains(got, contract.ReasonRecordIdentityConflict) {
		t.Fatalf("exact M0 reason leaked into metric labels:\n%s", got)
	}
}

func TestObservationCountsRemainSeparatedByStageDirectionAndResult(t *testing.T) {
	t.Parallel()

	recorder := NewRecorder(BuildInfo{})
	for _, observation := range []observability.Observation{
		{
			Component: observability.ComponentDetect,
			Stage:     observability.StageDetectCompleted,
			Result:    observability.ResultSuccess,
			Direction: observability.DirectionInternal,
			Counts:    observability.Counts{Records: 2},
		},
		{
			Component: observability.ComponentTrigger,
			Stage:     observability.StageTriggerCompleted,
			Result:    observability.ResultTerminal,
			Direction: observability.DirectionOutput,
			Counts:    observability.Counts{Records: 3},
		},
	} {
		recorder.Observe(context.Background(), observation)
	}
	got := scrape(t, recorder)
	for _, want := range []string{
		`bkmonitor_alarmd_observed_records_total{direction="internal",result="success",stage="detect_completed"} 2`,
		`bkmonitor_alarmd_observed_records_total{direction="output",result="terminal",stage="trigger_completed"} 3`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stage count is missing %q:\n%s", want, got)
		}
	}
}

func TestMetricNamesAndLabelsMatchApprovedContract(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	recorder.RecordProcess(StageTrigger, ModeShadow, StatusSuccess, ErrorNone, time.Second)
	recorder.RecordRecords(StageTrigger, ModeShadow, DirectionInput, RecordDetectionOutcome, 2)
	recorder.RecordPipelineLatency(StageDetect, StageTrigger, ModeShadow, time.Second)
	recorder.RecordShadowCompare(ComponentTrigger, CompareMatch)
	recorder.Observe(context.Background(), observability.Observation{
		Component:  observability.ComponentTrigger,
		Stage:      observability.StageTriggerCompleted,
		Result:     observability.ResultSuccess,
		Direction:  observability.DirectionOutput,
		ReasonCode: observability.ReasonNone,
		Duration:   time.Second,
	})

	got := scrape(t, recorder)
	wants := []string{
		"bkmonitor_alarmd_build_info",
		"bkmonitor_alarmd_process_duration_seconds_bucket",
		"bkmonitor_alarmd_process_total",
		"bkmonitor_alarmd_records_total",
		"bkmonitor_alarmd_pipeline_latency_seconds_bucket",
		"bkmonitor_alarmd_shadow_compare_total",
		"bkmonitor_alarmd_observation_total",
		"bkmonitor_alarmd_operation_total",
		"bkmonitor_alarmd_observation_duration_seconds_bucket",
		`error_code="none"`,
		`record_type="detection_outcome"`,
		`from_stage="detect"`,
		`to_stage="trigger"`,
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("metrics output does not contain %q", want)
		}
	}
}

func TestHistogramBucketsMatchPythonCompatibleContract(t *testing.T) {
	if got, want := processDurationBuckets, []float64{0.005, 0.01, 0.05, 0.1, 0.5, 1, 2.5, 5, 7.5, 10, 30}; !equalFloats(got, want) {
		t.Fatalf("process duration buckets = %v, want %v", got, want)
	}
	if got, want := pipelineLatencyBuckets, []float64{1, 2, 3, 5, 10, 15, 20, 30, 60, 180, 300}; !equalFloats(got, want) {
		t.Fatalf("pipeline latency buckets = %v, want %v", got, want)
	}
	if got, want := observationDurationBuckets, []float64{0.005, 0.01, 0.05, 0.1, 1, 30}; !equalFloats(got, want) {
		t.Fatalf("observation duration buckets = %v, want %v", got, want)
	}
}

func TestCustomMetricSeriesBudget(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	if err := recorder.BindLifecycle(&mutableLifecycleSource{snapshot: lifecycleBudgetSnapshot()}); err != nil {
		t.Fatalf("BindLifecycle() error = %v", err)
	}
	bindBudgetHealthAndResources(t, recorder)
	populateAllCustomLabelCombinations(recorder)
	got := countCustomSeries(t, recorder)
	if want := 18482; MaxCustomSeries() != want {
		t.Fatalf("MaxCustomSeries() = %d, want formula result %d", MaxCustomSeries(), want)
	}
	if gotObservation := countObservationSeries(t, recorder); gotObservation != observationCustomSeries() {
		t.Fatalf("observation series = %d, formula = %d", gotObservation, observationCustomSeries())
	}
	if got > MaxCustomSeries() {
		t.Fatalf("registered custom series = %d, calculated maximum = %d", got, MaxCustomSeries())
	}
	if MaxCustomSeries() > CustomSeriesBudget {
		t.Fatalf("calculated maximum custom series = %d, budget = %d", MaxCustomSeries(), CustomSeriesBudget)
	}
	if got > CustomSeriesBudget {
		t.Fatalf("maximum custom series = %d, budget = %d", got, CustomSeriesBudget)
	}
	if got <= 0 {
		t.Fatalf("maximum custom series must be positive, got %d", got)
	}
}

func TestCustomMetricDescriptorsAreExplicitlyApproved(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	if err := recorder.BindLifecycle(&mutableLifecycleSource{snapshot: lifecycleBudgetSnapshot()}); err != nil {
		t.Fatalf("BindLifecycle() error = %v", err)
	}
	expected := map[string]string{
		"bkmonitor_alarmd_build_info":                   "variableLabels: {version,commit,schema_version}",
		"bkmonitor_alarmd_process_duration_seconds":     "variableLabels: {stage,mode}",
		"bkmonitor_alarmd_process_total":                "variableLabels: {stage,mode,status,error_code}",
		"bkmonitor_alarmd_records_total":                "variableLabels: {stage,mode,direction,record_type}",
		"bkmonitor_alarmd_pipeline_latency_seconds":     "variableLabels: {from_stage,to_stage,mode}",
		"bkmonitor_alarmd_shadow_compare_total":         "variableLabels: {component,result}",
		"bkmonitor_alarmd_observation_total":            "variableLabels: {component,stage,result,reason_code}",
		"bkmonitor_alarmd_operation_total":              "variableLabels: {operation,result,reason_code}",
		"bkmonitor_alarmd_observation_duration_seconds": "variableLabels: {component,stage,result}",
		"bkmonitor_alarmd_observed_messages_total":      "variableLabels: {stage,direction,result}",
		"bkmonitor_alarmd_observed_records_total":       "variableLabels: {stage,direction,result}",
		"bkmonitor_alarmd_observed_plans_total":         "variableLabels: {stage,direction,result}",
		"bkmonitor_alarmd_observed_levels_total":        "variableLabels: {stage,direction,result}",
		"bkmonitor_alarmd_observed_events_total":        "variableLabels: {stage,direction,result}",
		"bkmonitor_alarmd_observed_bytes_total":         "variableLabels: {stage,direction,result}",
		"bkmonitor_alarmd_observed_keys_total":          "variableLabels: {stage,direction,result}",
		"bkmonitor_alarmd_observed_state_bytes_total":   "variableLabels: {stage,direction,result}",
		"bkmonitor_alarmd_ready":                        "variableLabels: {}",
		"bkmonitor_alarmd_assigned_claims":              "variableLabels: {}",
		"bkmonitor_alarmd_fatal_total":                  "variableLabels: {}",
		"bkmonitor_alarmd_draining":                     "variableLabels: {}",
		"bkmonitor_alarmd_drain_total":                  "variableLabels: {result}",
		"bkmonitor_alarmd_inflight_records":             "variableLabels: {}",
		"bkmonitor_alarmd_consumer_lag_records":         "variableLabels: {}",
	}

	descriptions := make(chan string)
	go func() {
		descriptors := make(chan *prometheus.Desc)
		go func() {
			recorder.registry.Describe(descriptors)
			close(descriptors)
		}()
		for descriptor := range descriptors {
			descriptions <- descriptor.String()
		}
		close(descriptions)
	}()

	seen := make(map[string]bool, len(expected))
	for description := range descriptions {
		name := metricNameFromDescriptor(description)
		if strings.HasPrefix(name, "go_") || strings.HasPrefix(name, "process_") {
			continue
		}
		labels, approved := expected[name]
		if !approved {
			t.Errorf("unapproved metric descriptor: %s", description)
			continue
		}
		seen[name] = true
		if !strings.Contains(description, "constLabels: {}") {
			t.Errorf("metric %s descriptor contains unapproved constant labels: %s", name, description)
		}
		if !strings.Contains(description, labels) {
			t.Errorf("metric %s descriptor = %q, want %q", name, description, labels)
		}
	}
	for name := range expected {
		if !seen[name] {
			t.Errorf("approved custom metric %s is not registered", name)
		}
	}
}

func lifecycleBudgetSnapshot() lifecycle.Snapshot {
	return lifecycle.Snapshot{ConsumerLagKnown: true}
}

func bindBudgetHealthAndResources(t *testing.T, recorder *Recorder) {
	t.Helper()
	health := observability.NewHealthTracker(observability.HealthSnapshot{
		State: observability.HealthReady, ConfigLoaded: true, SchemaReady: true,
		AssignmentReady: true, RuntimeStateReady: true, OutputSinkReady: true,
	})
	if err := recorder.BindHealth(health); err != nil {
		t.Fatalf("BindHealth() error = %v", err)
	}
	resources, err := observability.NewResourceGovernor(observability.ResourceGovernorConfig{})
	if err != nil {
		t.Fatalf("NewResourceGovernor() error = %v", err)
	}
	resources.Observe(observability.ResourceSnapshot{})
	if err := recorder.BindResources(resources); err != nil {
		t.Fatalf("BindResources() error = %v", err)
	}
}

func metricNameFromDescriptor(description string) string {
	const marker = `fqName: "`
	start := strings.Index(description, marker)
	if start < 0 {
		return ""
	}
	start += len(marker)
	end := strings.IndexByte(description[start:], '"')
	if end < 0 {
		return ""
	}
	return description[start : start+end]
}

func scrape(t *testing.T, recorder *Recorder) string {
	t.Helper()

	request := httptest.NewRequest("GET", "/metrics", nil)
	response := httptest.NewRecorder()
	promhttp.HandlerFor(recorder.Gatherer(), promhttp.HandlerOpts{}).ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("metrics status = %d, want 200", response.Code)
	}
	return response.Body.String()
}

func equalFloats(left, right []float64) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func populateAllCustomLabelCombinations(recorder *Recorder) {
	for _, stage := range allStages {
		for _, mode := range allModes {
			for _, status := range allStatuses {
				for _, code := range allErrors {
					recorder.RecordProcess(stage, mode, status, code, time.Second)
				}
			}
			for _, direction := range allDirections {
				for _, recordType := range allRecordTypes {
					recorder.RecordRecords(stage, mode, direction, recordType, 1)
				}
			}
		}
	}
	for _, edge := range allEdges {
		for _, mode := range allModes {
			recorder.RecordPipelineLatency(edge[0], edge[1], mode, time.Second)
		}
	}
	for _, component := range allComponents {
		for _, result := range allCompareResults {
			recorder.RecordShadowCompare(component, result)
		}
	}
	for _, pair := range observability.AllComponentStages() {
		for _, result := range observability.AllResults() {
			for _, reason := range observability.AllReasons(pair.Component) {
				recorder.Observe(context.Background(), observability.Observation{
					Component:  pair.Component,
					Stage:      pair.Stage,
					Result:     result,
					Direction:  observability.DirectionOther,
					ReasonCode: metricInputReason(reason),
					Duration:   time.Second,
				})
			}
		}
	}
	for _, operation := range observability.AllOperations() {
		for _, result := range observability.AllResults() {
			for _, reason := range observability.AllMetricReasons() {
				recorder.Observe(context.Background(), observability.Observation{
					Component: observability.ComponentResource, Stage: observability.StageResourceSoft,
					Result: result, Operation: operation, Direction: observability.DirectionOther,
					ReasonCode: metricInputReason(reason),
				})
			}
		}
	}
	for _, pair := range observability.AllComponentStages() {
		for _, direction := range observability.AllDirections() {
			for _, result := range observability.AllResults() {
				recorder.Observe(context.Background(), observability.Observation{
					Component: pair.Component, Stage: pair.Stage, Result: result, Direction: direction,
					Counts: observability.Counts{
						Messages: 1, Records: 1, Plans: 1, Levels: 1,
						Events: 1, Bytes: 1, Keys: 1, StateBytes: 1,
					},
				})
			}
		}
	}
}

func metricInputReason(metricReason observability.ReasonCode) observability.ReasonCode {
	switch metricReason {
	case observability.ReasonContractDeterministic:
		return observability.ReasonCode(contract.ReasonRecordInvalid)
	case observability.ReasonContractRetryable:
		return observability.ReasonCode(contract.ReasonKafkaUnavailable)
	case observability.ReasonContractCoverage:
		return observability.ReasonCode(contract.ReasonQueryPartial)
	default:
		return metricReason
	}
}

func countCustomSeries(t *testing.T, recorder *Recorder) int {
	return countSeries(t, recorder, func(name string) bool {
		return strings.HasPrefix(name, "bkmonitor_alarmd_")
	})
}

func countObservationSeries(t *testing.T, recorder *Recorder) int {
	return countSeries(t, recorder, func(name string) bool {
		return name == "bkmonitor_alarmd_observation_total" ||
			name == "bkmonitor_alarmd_operation_total" ||
			name == "bkmonitor_alarmd_observation_duration_seconds" ||
			strings.HasPrefix(name, "bkmonitor_alarmd_observed_")
	})
}

func countSeries(t *testing.T, recorder *Recorder, include func(string) bool) int {
	t.Helper()

	families, err := recorder.registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	total := 0
	for _, family := range families {
		if !include(family.GetName()) {
			continue
		}
		switch family.GetType() {
		case dto.MetricType_HISTOGRAM:
			for _, sample := range family.Metric {
				total += len(sample.GetHistogram().Bucket) + 3
			}
		case dto.MetricType_COUNTER, dto.MetricType_GAUGE:
			total += len(family.Metric)
		default:
			t.Fatalf("custom metric %s has unsupported type %s", family.GetName(), family.GetType())
		}
	}
	return total
}
