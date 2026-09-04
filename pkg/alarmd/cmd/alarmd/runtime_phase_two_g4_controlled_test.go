package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/shadow"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// These fixtures are controlled production-path Golden evidence. They prove
// the Go path itself; they are deliberately not classified as a natural Python
// comparison cohort.
func TestProductionPhaseTwoG4ControlledGoldenScenarios(t *testing.T) {
	tests := []struct {
		name       string
		strategyID int64
		kind       string
		metric     string
		table      string
		dimensions []string
		config     any
		family     observability.AlgorithmFamily
		detector   observability.AlgorithmDetectorKind
		inputs     []controlledAlgorithmInput
	}{
		{
			name: "simple ring ratio", strategyID: 4101, kind: strategy.DetectorKindSimpleRingRatio,
			metric: "usage", table: "system.cpu", dimensions: []string{"host"}, config: map[string]any{"floor": 50, "ceil": nil},
			family: observability.AlgorithmFamilySimpleRingRatio, detector: observability.AlgorithmDetectorKindSimpleRingRatio,
			inputs: []controlledAlgorithmInput{{observability.AlgorithmInputNamePrimary, observability.AlgorithmDependencyPointCurrent}, {observability.AlgorithmInputNameHistory, observability.AlgorithmDependencyPointPrevious}},
		},
		{
			name: "os restart", strategyID: 4102, kind: strategy.DetectorKindOsRestart,
			metric: "uptime", table: "system.env", dimensions: []string{"bk_target_cloud_id", "bk_target_ip"}, config: map[string]any{},
			family: observability.AlgorithmFamilyOsRestart, detector: observability.AlgorithmDetectorKindOsRestart,
			inputs: []controlledAlgorithmInput{
				{observability.AlgorithmInputNamePrimary, observability.AlgorithmDependencyPointCurrent},
				{observability.AlgorithmInputNameHistory, observability.AlgorithmDependencyPointPrevious},
				{observability.AlgorithmInputNameHistory, observability.AlgorithmDependencyPointTenMinute},
				{observability.AlgorithmInputNameHistory, observability.AlgorithmDependencyPointTwentyFiveMinute},
			},
		},
		{
			name: "proc port", strategyID: 4103, kind: strategy.DetectorKindProcPort,
			metric: "proc_exists", table: "system.proc_port", dimensions: []string{
				"protocol", "listen", "nonlisten", "not_accurate_listen", "bind_ip", "bk_target_ip", "bk_target_cloud_id", "display_name",
			}, config: map[string]any{}, family: observability.AlgorithmFamilyProcPort, detector: observability.AlgorithmDetectorKindProcPort,
			inputs: []controlledAlgorithmInput{{observability.AlgorithmInputNamePrimary, observability.AlgorithmDependencyPointCurrent}},
		},
		{
			name: "ping unreachable through threshold", strategyID: 4104, kind: controlplane.SourceAlgorithmTypePingUnreachable,
			metric: "loss_percent", table: "pingserver.base", dimensions: []string{"bk_target_ip"}, config: []any{},
			family: observability.AlgorithmFamilyPingUnreachable, detector: observability.AlgorithmDetectorKindThreshold,
			inputs: []controlledAlgorithmInput{{observability.AlgorithmInputNamePrimary, observability.AlgorithmDependencyPointCurrent}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runControlledG4Golden(t, test.strategyID, test.kind, test.metric, test.table, test.dimensions, test.config, test.family, test.detector, test.inputs)
		})
	}
}

func TestProductionPhaseTwoG4UnavailableQueryGroupDoesNotStopHealthySibling(t *testing.T) {
	t.Parallel()
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	failedID, healthyID := int64(4201), int64(4202)
	failed := controlledG4StrategyDocument(t, failedID, strategy.DetectorKindSimpleRingRatio, "usage", "system.cpu", []string{"host"}, map[string]any{"floor": 50, "ceil": nil})
	healthy := controlledG4StrategyDocument(t, healthyID, controlplane.SourceAlgorithmTypePingUnreachable, "loss_percent", "pingserver.base", []string{"bk_target_ip"}, []any{})
	if err := redisClient.Set(ctx, "alarm-config.strategy_ids", fmt.Sprintf("[%d,%d]", failedID, healthyID), 0).Err(); err != nil {
		t.Fatal(err)
	}
	for id, document := range map[int64]json.RawMessage{failedID: failed, healthyID: healthy} {
		if err := redisClient.Set(ctx, fmt.Sprintf("alarm-config.strategy_%d", id), string(document), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	base := controlledG4Base(t)
	var clock atomic.Int64
	clock.Store(base)
	now := func() time.Time { return time.Unix(clock.Load(), 0) }
	uqClient := &http.Client{Transport: controlledRoundTripper(func(request *http.Request) (*http.Response, error) {
		payload, err := decodeControlledG4UQRequest(request)
		if err != nil {
			return nil, err
		}
		if len(payload.QueryList) != 1 {
			return nil, fmt.Errorf("controlled sibling UQ query_list=%+v", payload.QueryList)
		}
		if payload.QueryList[0].TableID == "system.cpu" {
			return &http.Response{StatusCode: http.StatusBadGateway, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(nil)), Request: request}, nil
		}
		end, err := strconv.ParseInt(payload.EndTime, 10, 64)
		if err != nil {
			return nil, err
		}
		return controlledG4UQResponse(request, payload.QueryList[0].TableID,
			controlledG4UQSeries(base, end, payload.QueryList[0].TableID, payload.MetricMerge))
	})}
	cfg := controlledG4RuntimeConfig(address, "http://controlled-uq", "alarmd-g4-controlled-isolation")
	events := &recordingPhaseTwoEventSink{}
	var observationsMu sync.Mutex
	var observations []observability.Observation
	health := newPhaseTwoApplicationHealth()
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime), health,
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, HTTPClient: uqClient,
			AdditionalObserver: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				observationsMu.Lock()
				defer observationsMu.Unlock()
				observations = append(observations, observation)
			}),
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) { return events, nil },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := bundle.Shutdown(ctx); err != nil {
			t.Errorf("shutdown controlled sibling Bundle: %v", err)
		}
	}()
	if err := bundle.Start(ctx); err != nil {
		t.Fatal(err)
	}
	clock.Store(base + 1)
	if err := bundle.runScheduledOnce(ctx); err != nil {
		t.Fatalf("run controlled sibling Slot: %v", err)
	}

	production := bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
	queryGroups := make(map[string]execution.QueryGroupIdentity, 2)
	for _, queryGroup := range bundle.queryGroups {
		schedule, err := production.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, queryGroup)
		if err != nil || len(schedule.Plans) != 1 {
			t.Fatalf("controlled sibling schedule=%+v error=%v", schedule, err)
		}
		queryGroups[schedule.Plans[0].Identity.StrategyID] = queryGroup
	}
	failedProgress := loadPhaseTwoProgress(t, ctx, production, queryGroups[strconv.FormatInt(failedID, 10)])
	healthyProgress := loadPhaseTwoProgress(t, ctx, production, queryGroups[strconv.FormatInt(healthyID, 10)])
	if failedProgress.LastCompletionKind != execution.CompletionUnavailable || failedProgress.LastFullSlot != 0 || failedProgress.CurrentOrRecentGap == nil {
		t.Fatalf("unavailable G4 Progress=%+v", failedProgress)
	}
	if healthyProgress.LastCompletionKind != execution.CompletionFull || healthyProgress.LastFullSlot != execution.EvaluationTime(base) {
		t.Fatalf("healthy G4 Progress=%+v", healthyProgress)
	}
	written := events.snapshot()
	if len(written) != 1 || written[0].PlanRef.StrategyID != strconv.FormatInt(healthyID, 10) || written[0].EventKind != "ABNORMAL" {
		t.Fatalf("isolated G4 events=%+v", written)
	}
	observationsMu.Lock()
	captured := append([]observability.Observation(nil), observations...)
	observationsMu.Unlock()
	foundEvaluation, foundInput := false, false
	for _, observation := range captured {
		observation = observability.NormalizeObservation(observation)
		for _, fact := range observation.AlgorithmEvaluations {
			foundEvaluation = foundEvaluation || fact.SourceAlgorithmFamily == observability.AlgorithmFamilyPingUnreachable &&
				fact.DetectorKind == observability.AlgorithmDetectorKindThreshold && fact.Result == observability.AlgorithmEvaluationResultAbnormal
		}
		for _, fact := range observation.AlgorithmInputs {
			foundInput = foundInput || fact.SourceAlgorithmFamily == observability.AlgorithmFamilyPingUnreachable &&
				fact.InputName == observability.AlgorithmInputNamePrimary && fact.DependencyPoint == observability.AlgorithmDependencyPointCurrent &&
				fact.Result == observability.AlgorithmInputResultAvailable
		}
	}
	if !foundEvaluation || !foundInput {
		t.Fatalf("healthy G4 algorithm evidence evaluation/input=%t/%t", foundEvaluation, foundInput)
	}
	assertObservedOrder(t, observedStages(captured), []observability.Stage{
		observability.StageEvaluationCompleted, observability.StageEventACKed, observability.StageStateApplied, observability.StageProgressCommitted,
	})
	if snapshot := health.HealthSnapshot(); snapshot.State != observability.HealthReady || len(bundle.runners) != 2 {
		t.Fatalf("controlled sibling health=%+v runners=%d", snapshot, len(bundle.runners))
	}
}

type controlledAlgorithmInput struct {
	name       observability.AlgorithmInputName
	dependency observability.AlgorithmDependencyPoint
}

func runControlledG4Golden(
	t *testing.T,
	strategyID int64,
	kind, metricName, table string,
	dimensions []string,
	algorithmConfig any,
	family observability.AlgorithmFamily,
	detector observability.AlgorithmDetectorKind,
	wantInputs []controlledAlgorithmInput,
) {
	t.Helper()
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	document := controlledG4StrategyDocument(t, strategyID, kind, metricName, table, dimensions, algorithmConfig)
	if err := redisClient.Set(ctx, "alarm-config.strategy_ids", fmt.Sprintf("[%d]", strategyID), 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, fmt.Sprintf("alarm-config.strategy_%d", strategyID), string(document), 0).Err(); err != nil {
		t.Fatal(err)
	}

	base := controlledG4Base(t)
	var clock atomic.Int64
	clock.Store(base)
	now := func() time.Time { return time.Unix(clock.Load(), 0) }
	uqClient := controlledG4UQClient(t, base, table)

	cfg := controlledG4RuntimeConfig(address, "http://controlled-uq", fmt.Sprintf("alarmd-g4-controlled-%d", strategyID))
	events := &recordingPhaseTwoEventSink{}
	var observationsMu sync.Mutex
	var observations []observability.Observation
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, HTTPClient: uqClient,
			AdditionalObserver: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				observationsMu.Lock()
				defer observationsMu.Unlock()
				observations = append(observations, observation)
			}),
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) { return events, nil },
		},
	)
	if err != nil {
		t.Fatalf("open controlled production Bundle: %v", err)
	}
	defer func() {
		if err := bundle.Shutdown(ctx); err != nil {
			t.Errorf("shutdown controlled production Bundle: %v", err)
		}
	}()
	if err := bundle.Start(ctx); err != nil {
		t.Fatalf("start controlled production Bundle: %v", err)
	}
	for _, evaluationTime := range []int64{base, base + 60} {
		clock.Store(evaluationTime + 1)
		if err := bundle.runScheduledOnce(ctx); err != nil {
			t.Fatalf("run controlled Slot %d: %v", evaluationTime, err)
		}
	}

	written := events.snapshot()
	if len(written) != 2 || written[0].EventKind != "ABNORMAL" || written[1].EventKind != "RECOVERY" {
		observationsMu.Lock()
		captured := append([]observability.Observation(nil), observations...)
		observationsMu.Unlock()
		var facts []observability.AlgorithmEvaluationFact
		var inputFacts []observability.AlgorithmInputFact
		var stages []string
		for _, observation := range captured {
			facts = append(facts, observation.AlgorithmEvaluations...)
			inputFacts = append(inputFacts, observation.AlgorithmInputs...)
			stages = append(stages, fmt.Sprintf("%s/%s/%s/%v", observation.Stage, observation.Result, observation.ReasonCode, observation.Err))
		}
		t.Fatalf("controlled TriggerEvent kinds=%v algorithm facts=%+v input facts=%+v stages=%v, want ABNORMAL then RECOVERY", controlledEventKinds(written), facts, inputFacts, stages)
	}
	var episode shadow.Episode
	for index, event := range written {
		projection, err := shadow.ProjectTriggerEventV1(shadow.ChainGo, event)
		if err != nil {
			t.Fatalf("ProjectTriggerEventV1(event %d): %v", index, err)
		}
		var transition shadow.EpisodeTransition
		episode, transition, err = shadow.ApplyMatchedEvent(episode, projection)
		if err != nil {
			t.Fatalf("ApplyMatchedEvent(event %d): %v", index, err)
		}
		want := shadow.EpisodeOpened
		if index == 1 {
			want = shadow.EpisodeClosed
		}
		if transition != want {
			t.Fatalf("controlled Golden transition[%d]=%s, want %s", index, transition, want)
		}
	}

	if len(bundle.queryGroups) != 1 {
		t.Fatalf("controlled assigned Query Groups=%v, want one", bundle.queryGroups)
	}
	production, ok := bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
	if !ok {
		t.Fatalf("production ownership type=%T", bundle.dependencies.Ownership)
	}
	progress := loadPhaseTwoProgress(t, ctx, production, bundle.queryGroups[0])
	if progress.LastFullSlot != execution.EvaluationTime(base+60) || progress.NextSlot != execution.EvaluationTime(base+120) {
		t.Fatalf("controlled Progress=%+v", progress)
	}
	stateKeys, err := redisClient.Keys(ctx, cfg.Redis.StatePrefix+":runtime:v2:*").Result()
	if err != nil || len(stateKeys) == 0 {
		t.Fatalf("controlled Runtime State keys=%v error=%v", stateKeys, err)
	}

	observationsMu.Lock()
	captured := append([]observability.Observation(nil), observations...)
	observationsMu.Unlock()
	assertControlledG4Observations(t, captured, family, detector, wantInputs)
}

func controlledEventKinds(events []contract.TriggerEventV1) []string {
	kinds := make([]string, len(events))
	for index := range events {
		kinds[index] = events[index].EventKind
	}
	return kinds
}

func controlledG4RuntimeConfig(address, endpoint, prefix string) config.Config {
	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = prefix
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = endpoint
	cfg.PhaseTwo.Access.MinReadyDelay = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.DownstreamExecutionReserve = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Worker.RegistrationRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Ownership.LeaseRenewInterval = config.Duration(time.Minute)
	return cfg
}

func controlledG4StrategyDocument(
	t *testing.T,
	id int64,
	kind, metricName, table string,
	dimensions []string,
	algorithmConfig any,
) json.RawMessage {
	t.Helper()
	query := map[string]any{
		"data_source_label": "bk_monitor", "data_type_label": "time_series", "metric_field": metricName,
		"alias": "a", "agg_dimension": dimensions, "agg_method": "MAX", "agg_interval": 60, "result_table_id": table,
	}
	if metricID := map[string]string{
		strategy.DetectorKindOsRestart:                  "bk_monitor.os_restart",
		strategy.DetectorKindProcPort:                   "bk_monitor.proc_port",
		controlplane.SourceAlgorithmTypePingUnreachable: "bk_monitor.ping-gse",
	}[kind]; metricID != "" {
		query["metric_id"] = metricID
	}
	item := map[string]any{
		"id": 1, "query_md5": fmt.Sprintf("g4-controlled-%d", id), "expression": "a", "unit": "",
		"query_configs": []any{query},
		"algorithms":    []any{map[string]any{"level": 1, "type": kind, "config": algorithmConfig}},
	}
	if kind == strategy.DetectorKindOsRestart {
		item["functions"] = []any{map[string]any{"id": "abs", "params": []any{}}}
	}
	document, err := json.Marshal(map[string]any{
		"id": id, "bk_biz_id": 2, "bk_tenant_id": "tenant-a", "space_uid": "bkcc__2", "update_time": 1,
		"items": []any{item},
		"detects": []any{map[string]any{
			"level": 1, "priority": 1, "connector": "and",
			"trigger_config":  map[string]any{"count": 1, "check_window": 1},
			"recovery_config": map[string]any{"check_window": 1},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return document
}

type controlledG4UQRequest struct {
	QueryList []struct {
		TableID string `json:"table_id"`
	} `json:"query_list"`
	MetricMerge string `json:"metric_merge"`
	StartTime   string `json:"start_time"`
	EndTime     string `json:"end_time"`
}

type controlledRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip controlledRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func controlledG4UQClient(t *testing.T, base int64, table string) *http.Client {
	t.Helper()
	return &http.Client{Transport: controlledRoundTripper(func(request *http.Request) (*http.Response, error) {
		payload, err := decodeControlledG4UQRequest(request)
		if err != nil {
			return nil, err
		}
		end, err := strconv.ParseInt(payload.EndTime, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("controlled UQ end_time=%q: %w", payload.EndTime, err)
		}
		return controlledG4UQResponse(request, table, controlledG4UQSeries(base, end, table, payload.MetricMerge))
	})}
}

func decodeControlledG4UQRequest(request *http.Request) (controlledG4UQRequest, error) {
	var payload controlledG4UQRequest
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		return controlledG4UQRequest{}, fmt.Errorf("decode controlled UQ request: %w", err)
	}
	return payload, nil
}

func controlledG4UQResponse(request *http.Request, table string, series map[string]any) (*http.Response, error) {
	var encoded bytes.Buffer
	if err := json.NewEncoder(&encoded).Encode(map[string]any{
		"series": []any{series}, "status": nil, "trace_id": "g4-controlled-uq", "is_partial": false,
		"result_table_id": []string{table},
	}); err != nil {
		return nil, fmt.Errorf("encode controlled UQ response: %w", err)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(encoded.Bytes())), Request: request}, nil
}

func controlledG4Base(t *testing.T) int64 {
	t.Helper()
	wall := time.Now()
	if remaining := time.Until(wall.Truncate(time.Minute).Add(time.Minute)); remaining < 10*time.Second {
		time.Sleep(remaining + 10*time.Millisecond)
		wall = time.Now()
	}
	base := wall.Unix()
	return base - base%60
}

func controlledG4UQSeries(base, end int64, table, metricMerge string) map[string]any {
	groupKeys := []string{"host"}
	groupValues := []string{"controlled-host"}
	values := []any{}
	switch table {
	case "system.cpu":
		value := 100.0
		if end == base {
			value = 40
		}
		values = []any{[]any{(end - 1) * 1000, value}}
	case "system.env":
		groupKeys = []string{"bk_target_cloud_id", "bk_target_ip"}
		groupValues = []string{"0", "127.0.0.1"}
		if metricMerge == "a <= 3600" {
			current := 100.0
			if end > base {
				current = 700
			}
			values = []any{[]any{(end - 1) * 1000, current}}
		} else {
			values = []any{
				[]any{(end - 1501) * 1000, 2000.0}, []any{(end - 601) * 1000, 1000.0}, []any{(end - 61) * 1000, 200.0},
			}
		}
	case "system.proc_port":
		groupKeys = []string{"protocol", "listen", "nonlisten", "not_accurate_listen", "bind_ip", "bk_target_ip", "bk_target_cloud_id", "display_name"}
		groupValues = []string{"tcp", "[]", "[]", "[]", "127.0.0.1", "127.0.0.1", "0", "controlled-proc"}
		value := 0.0
		if end > base {
			value = 1
		}
		values = []any{[]any{(end - 1) * 1000, value}}
	case "pingserver.base":
		groupKeys = []string{"bk_target_ip"}
		groupValues = []string{"127.0.0.1"}
		value := 1.0
		if end > base {
			value = 0
		}
		values = []any{[]any{(end - 1) * 1000, value}}
	}
	return map[string]any{
		"name": "_result0", "columns": []string{"_time", "_result"}, "types": []string{"int64", "float64"},
		"group_keys": groupKeys, "group_values": groupValues, "values": values,
	}
}

func assertControlledG4Observations(
	t *testing.T,
	observations []observability.Observation,
	family observability.AlgorithmFamily,
	detector observability.AlgorithmDetectorKind,
	wantInputs []controlledAlgorithmInput,
) {
	t.Helper()
	results := make([]observability.AlgorithmEvaluationResult, 0, 2)
	inputs := make(map[controlledAlgorithmInput]map[observability.AlgorithmInputResult]bool)
	for _, observation := range observations {
		observation = observability.NormalizeObservation(observation)
		for _, fact := range observation.AlgorithmEvaluations {
			if fact.SourceAlgorithmFamily == family {
				if fact.DetectorKind != detector {
					t.Fatalf("algorithm detector=%q, want %q", fact.DetectorKind, detector)
				}
				results = append(results, fact.Result)
			}
		}
		for _, fact := range observation.AlgorithmInputs {
			if fact.SourceAlgorithmFamily != family {
				continue
			}
			key := controlledAlgorithmInput{fact.InputName, fact.DependencyPoint}
			if inputs[key] == nil {
				inputs[key] = make(map[observability.AlgorithmInputResult]bool)
			}
			inputs[key][fact.Result] = true
		}
	}
	if len(results) != 2 || results[0] != observability.AlgorithmEvaluationResultAbnormal || results[1] != observability.AlgorithmEvaluationResultRecovery {
		t.Fatalf("algorithm results=%v, want abnormal then recovery", results)
	}
	for _, input := range wantInputs {
		if !inputs[input][observability.AlgorithmInputResultAvailable] {
			t.Fatalf("algorithm input %+v facts=%v, want available", input, inputs[input])
		}
	}
	assertObservedOrder(t, observedStages(observations), []observability.Stage{
		observability.StageEvaluationCompleted, observability.StageEventACKed, observability.StageStateApplied, observability.StageProgressCommitted,
		observability.StageEvaluationCompleted, observability.StageEventACKed, observability.StageStateApplied, observability.StageProgressCommitted,
	})
}
