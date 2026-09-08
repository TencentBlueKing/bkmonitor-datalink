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

// metricFamilySeriesDevelopmentLimit is an initial development guard, not a
// runtime danger threshold.
const metricFamilySeriesDevelopmentLimit = 50000

func TestCustomMetricFamilySeriesDevelopmentLimits(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	if err := recorder.BindLifecycle(&mutableLifecycleSource{snapshot: lifecycleBudgetSnapshot()}); err != nil {
		t.Fatalf("BindLifecycle() error = %v", err)
	}
	bindBudgetHealthAndResources(t, recorder)
	populateAllCustomLabelCombinations(recorder)

	bounds := customMetricFamilySeriesUpperBounds()
	descriptors := customMetricDescriptorNames(t, recorder)
	for family, bound := range bounds {
		if bound <= 0 || bound > metricFamilySeriesDevelopmentLimit {
			t.Errorf("metric family %s theoretical maximum = %d, development limit = %d",
				family, bound, metricFamilySeriesDevelopmentLimit)
		}
		if !descriptors[family] {
			t.Errorf("metric family bound %s has no registered descriptor", family)
		}
	}
	for family := range descriptors {
		if _, ok := bounds[family]; !ok {
			t.Errorf("registered custom metric family %s has no series upper bound", family)
		}
	}
	for family, got := range countCustomSeriesByFamily(t, recorder) {
		if got > bounds[family] {
			t.Errorf("metric family %s registered series = %d, theoretical maximum = %d", family, got, bounds[family])
		}
	}

	for family, want := range map[string]int{
		"bkmonitor_alarmd_process_duration_seconds":     126,
		"bkmonitor_alarmd_pipeline_latency_seconds":     84,
		"bkmonitor_alarmd_observation_duration_seconds": 2970,
	} {
		if got := bounds[family]; got != want {
			t.Errorf("histogram family %s theoretical maximum = %d, want buckets/+Inf/sum/count total %d", family, got, want)
		}
	}
	if got, want := bounds["bkmonitor_alarmd_health_last_progress_timestamp_seconds"],
		len(observability.AllStages()); got != want {
		t.Errorf("health last-progress stage maximum = %d, want complete legal stage catalog %d", got, want)
	}
	for family, want := range map[string]int{
		"bkmonitor_alarmd_worker_ready_queue":           2,
		"bkmonitor_alarmd_worker_query_inflight":        4,
		"bkmonitor_alarmd_worker_query_admission_total": 20,
		// Redis command names are the bounded redisCommandNames set plus
		// "other", each with pipelined true/false.
		"bkmonitor_alarmd_redis_command_total":            (len(redisCommandNames) + 1) * 2,
		"bkmonitor_alarmd_redis_command_failure_total":    (len(redisCommandNames) + 1) * 2,
		"bkmonitor_alarmd_redis_command_duration_seconds": (len(redisCommandNames) + 1) * 2 * (12 + 1 + 2),
	} {
		if got := bounds[family]; got != want {
			t.Errorf("query permit metric family %s theoretical maximum = %d, want %d", family, got, want)
		}
	}
}

func TestCustomMetricDescriptorsAreExplicitlyApproved(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	if err := recorder.BindLifecycle(&mutableLifecycleSource{snapshot: lifecycleBudgetSnapshot()}); err != nil {
		t.Fatalf("BindLifecycle() error = %v", err)
	}
	expected := map[string]string{
		"bkmonitor_alarmd_build_info":                                   "variableLabels: {version,commit,schema_version}",
		"bkmonitor_alarmd_process_duration_seconds":                     "variableLabels: {stage,mode}",
		"bkmonitor_alarmd_process_total":                                "variableLabels: {stage,mode,status,error_code}",
		"bkmonitor_alarmd_records_total":                                "variableLabels: {stage,mode,direction,record_type}",
		"bkmonitor_alarmd_pipeline_latency_seconds":                     "variableLabels: {from_stage,to_stage,mode}",
		"bkmonitor_alarmd_shadow_compare_total":                         "variableLabels: {component,result}",
		"bkmonitor_alarmd_observation_total":                            "variableLabels: {component,stage,result,reason_code}",
		"bkmonitor_alarmd_operation_total":                              "variableLabels: {operation,result,reason_code}",
		"bkmonitor_alarmd_observation_duration_seconds":                 "variableLabels: {component,stage,result}",
		"bkmonitor_alarmd_observed_messages_total":                      "variableLabels: {stage,direction,result}",
		"bkmonitor_alarmd_observed_records_total":                       "variableLabels: {stage,direction,result}",
		"bkmonitor_alarmd_observed_plans_total":                         "variableLabels: {stage,direction,result}",
		"bkmonitor_alarmd_observed_levels_total":                        "variableLabels: {stage,direction,result}",
		"bkmonitor_alarmd_observed_events_total":                        "variableLabels: {stage,direction,result}",
		"bkmonitor_alarmd_observed_bytes_total":                         "variableLabels: {stage,direction,result}",
		"bkmonitor_alarmd_observed_keys_total":                          "variableLabels: {stage,direction,result}",
		"bkmonitor_alarmd_observed_state_bytes_total":                   "variableLabels: {stage,direction,result}",
		"bkmonitor_alarmd_message_receipt_status_total":                 "variableLabels: {status}",
		"bkmonitor_alarmd_message_receipt_business_total":               "variableLabels: {field}",
		"bkmonitor_alarmd_message_receipt_delivery_total":               "variableLabels: {outcome}",
		"bkmonitor_alarmd_worker_work_total":                            "variableLabels: {work_kind}",
		"bkmonitor_alarmd_worker_busy_seconds_total":                    "variableLabels: {stage}",
		"bkmonitor_alarmd_last_progress_timestamp_seconds":              "variableLabels: {kind}",
		"bkmonitor_alarmd_capacity_transition_total":                    "variableLabels: {budget,result}",
		"bkmonitor_alarmd_worker_owned_query_groups":                    "variableLabels: {worker_role}",
		"bkmonitor_alarmd_ownership_transition_total":                   "variableLabels: {transition,result,reason_class}",
		"bkmonitor_alarmd_source_observation_total":                     "variableLabels: {source_kind,result,reason_class}",
		"bkmonitor_alarmd_source_refresh_total":                         "variableLabels: {status}",
		"bkmonitor_alarmd_activation_failure_total":                     "variableLabels: {activation_failure_stage,activation_failure_class}",
		"bkmonitor_alarmd_worker_ready_queue":                           "variableLabels: {kind}",
		"bkmonitor_alarmd_worker_query_inflight":                        "variableLabels: {kind}",
		"bkmonitor_alarmd_worker_query_admission_total":                 "variableLabels: {operation,result}",
		"bkmonitor_alarmd_control_cache_total":                          "variableLabels: {object,result}",
		"bkmonitor_alarmd_legacy_pod_cache_total":                       "variableLabels: {result}",
		"bkmonitor_alarmd_redis_operation_total":                        "variableLabels: {}",
		"bkmonitor_alarmd_redis_pool_size":                              "variableLabels: {client}",
		"bkmonitor_alarmd_redis_pool_connections":                       "variableLabels: {client,state}",
		"bkmonitor_alarmd_redis_pool_waits_total":                       "variableLabels: {client,result}",
		"bkmonitor_alarmd_redis_command_total":                          "variableLabels: {command,pipelined}",
		"bkmonitor_alarmd_redis_command_failure_total":                  "variableLabels: {command,pipelined}",
		"bkmonitor_alarmd_redis_command_duration_seconds":               "variableLabels: {command,pipelined}",
		"bkmonitor_alarmd_short_period_slot_completions_total":          "variableLabels: {cohort,operation,completion_kind}",
		"bkmonitor_alarmd_short_period_slot_execution_duration_seconds": "variableLabels: {cohort}",
		"bkmonitor_alarmd_short_period_slot_completion_lag_seconds":     "variableLabels: {cohort}",
		"bkmonitor_alarmd_run_one_return_total":                         "variableLabels: {outcome}",
		"bkmonitor_alarmd_expired_range_total":                          "variableLabels: {result}",
		"bkmonitor_alarmd_expired_slots_finalized_total":                "variableLabels: {reason}",
		"bkmonitor_alarmd_execute_return_total":                         "variableLabels: {outcome}",
		"bkmonitor_alarmd_progress_completed_total":                     "variableLabels: {kind}",
		"bkmonitor_alarmd_run_one_attempted_total":                      "variableLabels: {}",
		"bkmonitor_alarmd_scheduler_active_executions":                  "variableLabels: {}",
		"bkmonitor_alarmd_scheduler_ready_runners":                      "variableLabels: {}",
		"bkmonitor_alarmd_scheduler_delayed_runners":                    "variableLabels: {}",
		"bkmonitor_alarmd_query_permit_wait_seconds":                    "variableLabels: {queue_kind}",
		"bkmonitor_alarmd_slot_operation_duration_seconds":              "variableLabels: {stage}",
		"bkmonitor_alarmd_active_qg_set_query_groups":                   "variableLabels: {}",
		"bkmonitor_alarmd_active_qg_set_object_bytes":                   "variableLabels: {}",
		"bkmonitor_alarmd_active_qg_set_encode_duration_seconds":        "variableLabels: {result}",
		"bkmonitor_alarmd_active_qg_set_redis_duration_seconds":         "variableLabels: {operation,result}",
		"bkmonitor_alarmd_legacy_active_qg_migration_total":             "variableLabels: {result,reason_class}",
		"bkmonitor_alarmd_legacy_active_qg_migration_scan_keys":         "variableLabels: {}",
		"bkmonitor_alarmd_legacy_active_qg_migration_duration_seconds":  "variableLabels: {result}",
		"bkmonitor_alarmd_undrained_draining_query_groups":              "variableLabels: {}",
		"bkmonitor_alarmd_ready":                                        "variableLabels: {}",
		"bkmonitor_alarmd_assigned_claims":                              "variableLabels: {}",
		"bkmonitor_alarmd_fatal_total":                                  "variableLabels: {}",
		"bkmonitor_alarmd_draining":                                     "variableLabels: {}",
		"bkmonitor_alarmd_drain_total":                                  "variableLabels: {result}",
		"bkmonitor_alarmd_inflight_records":                             "variableLabels: {}",
		"bkmonitor_alarmd_consumer_lag_records":                         "variableLabels: {}",
	}
	expected["bkmonitor_alarmd_algorithm_evaluation_total"] = "variableLabels: {algorithm_family,result}"
	expected["bkmonitor_alarmd_algorithm_input_total"] = "variableLabels: {algorithm_family,input_name,dependency_point,result}"

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
	for _, pair := range observability.AllMetricComponentStages() {
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
	for _, operation := range observability.AllMetricOperations() {
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
	for _, pair := range observability.AllMetricComponentStages() {
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

func customMetricFamilySeriesUpperBounds() map[string]int {
	fqName := func(name string) string {
		return prometheus.BuildFQName(metricNamespace, metricSubsystem, name)
	}
	histogramSeries := func(labelCombinations, explicitBuckets int) int {
		return labelCombinations * (explicitBuckets + 1 + 2) // explicit buckets, +Inf, sum and count
	}
	// Bounded command names plus "other", each with pipelined true and false.
	redisCommandSeries := (len(redisCommandNames) + 1) * 2
	metricReasonSeries := func(component observability.Component, result observability.Result) int {
		count := len(observability.AllReasons(component))
		if result != observability.ResultStarted && result != observability.ResultSuccess &&
			result != observability.ResultResumed {
			count-- // ReasonNone normalizes to internal_unknown for non-success results.
		}
		return count
	}
	metricReasonSets := func(component observability.Component) int {
		total := 0
		for _, result := range observability.AllResults() {
			total += metricReasonSeries(component, result)
		}
		return total
	}

	observationTotal := 0
	for _, pair := range observability.AllMetricComponentStages() {
		for _, result := range observability.AllResults() {
			observationTotal += metricReasonSeries(pair.Component, result)
		}
	}
	observationCount := len(observability.AllMetricStages()) * len(observability.AllDirections()) *
		len(observability.AllResults())

	bounds := map[string]int{
		fqName("build_info"):               1,
		fqName("process_duration_seconds"): histogramSeries(len(allStages)*len(allModes), len(processDurationBuckets)),
		fqName("process_total"):            len(allStages) * len(allModes) * len(allStatuses) * len(allErrors),
		fqName("records_total"):            len(allStages) * len(allModes) * len(allDirections) * len(allRecordTypes),
		fqName("pipeline_latency_seconds"): histogramSeries(len(allEdges)*len(allModes), len(pipelineLatencyBuckets)),
		fqName("shadow_compare_total"):     len(allComponents) * len(allCompareResults),

		fqName("observation_total"): observationTotal,
		fqName("operation_total"): len(observability.AllMetricOperations()) *
			metricReasonSets(observability.ComponentResource),
		fqName("observation_duration_seconds"): histogramSeries(
			len(observability.AllMetricComponentStages())*len(observability.AllResults()),
			len(observationDurationBuckets),
		),

		fqName("message_receipt_status_total"):   len(receiptStatuses),
		fqName("message_receipt_business_total"): len(receiptBusinessFields),
		fqName("message_receipt_delivery_total"): 3,

		fqName("worker_work_total"):               len(phaseTwoWorkKinds),
		fqName("worker_busy_seconds_total"):       len(phaseTwoBusyStages),
		fqName("last_progress_timestamp_seconds"): len(phaseTwoProgressKinds),
		fqName("capacity_transition_total"):       len(phaseTwoBudgets) * len(phaseTwoCapacityResults),
		fqName("source_observation_total"):        len(observability.AllSourceKinds()) * len(phaseTwoSourceResults) * len(observability.AllReasons(observability.ComponentControlPlane)),
		fqName("source_refresh_total"):            len(observability.AllSourceRefreshStatuses()),
		fqName("activation_failure_total"):        len(observability.AllActivationFailureStages()) * len(observability.AllActivationFailureClasses()),
		fqName("worker_owned_query_groups"):       1,
		fqName("ownership_transition_total"):      len(phaseTwoOwnershipTransitions) * metricReasonSets(observability.ComponentOwnership),
		fqName("worker_ready_queue"):              len(phaseTwoReadyQueueKinds),
		fqName("worker_query_inflight"):           len(phaseTwoQueryInflightKinds),
		fqName("worker_query_admission_total"):    len(phaseTwoQueryInflightKinds) * len(phaseTwoQueryAdmissionResults),
		// Four cached objects: version, snapshot, activation, timeline.
		fqName("control_cache_total"):    12,
		fqName("legacy_pod_cache_total"): 3,
		// Two clients at most: the control plane connection and, when it resolves
		// to a different endpoint, the runtime connection.
		// One unlabelled series; connection acquisitions minus it is the retries.
		fqName("redis_operation_total"):                        1,
		fqName("redis_pool_size"):                              2,
		fqName("redis_pool_connections"):                       6,
		fqName("redis_pool_waits_total"):                       6,
		fqName("redis_command_total"):                          redisCommandSeries,
		fqName("redis_command_failure_total"):                  redisCommandSeries,
		fqName("redis_command_duration_seconds"):               histogramSeries(redisCommandSeries, 12),
		fqName("short_period_slot_completions_total"):          56,
		fqName("short_period_slot_execution_duration_seconds"): 24,
		fqName("short_period_slot_completion_lag_seconds"):     24,
		fqName("run_one_return_total"):                         13,
		fqName("expired_range_total"):                          4,
		fqName("expired_slots_finalized_total"):                2,
		fqName("execute_return_total"):                         6,
		fqName("progress_completed_total"):                     7,
		fqName("run_one_attempted_total"):                      1,
		fqName("scheduler_active_executions"):                  1,
		fqName("scheduler_ready_runners"):                      1,
		fqName("scheduler_delayed_runners"):                    1,
		fqName("query_permit_wait_seconds"):                    22,
		fqName("slot_operation_duration_seconds"):              55,
		fqName("active_qg_set_query_groups"):                   1,
		fqName("active_qg_set_object_bytes"):                   1,
		fqName("active_qg_set_encode_duration_seconds"):        histogramSeries(2, len(activeQGSetDurationBuckets)),
		fqName("active_qg_set_redis_duration_seconds"):         histogramSeries(3*2, len(activeQGSetDurationBuckets)),
		fqName("legacy_active_qg_migration_total"):             3 * 11,
		fqName("legacy_active_qg_migration_scan_keys"):         histogramSeries(1, len(legacyMigrationScanBuckets)),
		fqName("legacy_active_qg_migration_duration_seconds"):  histogramSeries(3, len(activeQGSetDurationBuckets)),
		fqName("undrained_draining_query_groups"):              1,
		fqName("ready"):                                        1,
		fqName("assigned_claims"):                              1,
		fqName("fatal_total"):                                  1,
		fqName("draining"):                                     1,
		fqName("drain_total"):                                  int(lifecycle.DrainResultCount),
		fqName("inflight_records"):                             1,
		fqName("consumer_lag_records"):                         1,
		fqName("health_ready"):                                 1,
		fqName("health_state"):                                 len(observability.AllHealthStates()),
		fqName("health_reason"):                                len(observability.AllReasons(observability.ComponentResource)),
		fqName("health_assigned_claims"):                       1,
		fqName("health_inflight_messages"):                     1,
		fqName("health_worker_queue_depth"):                    1,
		fqName("health_worker_queue_bytes"):                    1,
		fqName("health_consumer_lag_records"):                  1,
		fqName("health_last_progress_timestamp_seconds"):       len(observability.AllStages()),
		fqName("health_last_recovery_timestamp_seconds"):       1,
		fqName("resource_state"):                               len(observability.AllResourceStates()),
	}
	bounds[fqName("algorithm_evaluation_total")] = 25
	bounds[fqName("algorithm_input_total")] = 160
	for _, name := range []string{
		"messages", "records", "plans", "levels", "events", "bytes", "keys", "state_bytes",
	} {
		bounds[fqName("observed_"+name+"_total")] = observationCount
	}
	for _, name := range []string{
		"cpu_cores", "rss_bytes", "heap_bytes", "gc_pause_seconds", "worker_queue_depth",
		"worker_queue_bytes", "inflight_messages", "inflight_bytes", "consumer_lag_records", "state_bytes",
	} {
		bounds[fqName("resource_"+name)] = 1
	}
	return bounds
}

func customMetricDescriptorNames(t *testing.T, recorder *Recorder) map[string]bool {
	t.Helper()
	descriptors := make(chan *prometheus.Desc)
	go func() {
		recorder.registry.Describe(descriptors)
		close(descriptors)
	}()
	names := make(map[string]bool)
	for descriptor := range descriptors {
		name := metricNameFromDescriptor(descriptor.String())
		if strings.HasPrefix(name, "bkmonitor_alarmd_") {
			names[name] = true
		}
	}
	return names
}

func countCustomSeriesByFamily(t *testing.T, recorder *Recorder) map[string]int {
	t.Helper()

	families, err := recorder.registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	counts := make(map[string]int)
	for _, family := range families {
		name := family.GetName()
		if !strings.HasPrefix(name, "bkmonitor_alarmd_") {
			continue
		}
		switch family.GetType() {
		case dto.MetricType_HISTOGRAM:
			for _, sample := range family.Metric {
				counts[name] += len(sample.GetHistogram().Bucket) + 3
			}
		case dto.MetricType_COUNTER, dto.MetricType_GAUGE:
			counts[name] += len(family.Metric)
		default:
			t.Fatalf("custom metric %s has unsupported type %s", name, family.GetType())
		}
	}
	return counts
}
