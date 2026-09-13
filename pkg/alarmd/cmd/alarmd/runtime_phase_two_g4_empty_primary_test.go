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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// An OsRestart Plan issues two physical queries: the PRIMARY uptime query
// (metric_merge "a <= 3600") and the raw uptime history query. When no host
// restarted, the PRIMARY query returns FULL EMPTY while the history query
// returns DATA for every host. The Plan then has no series and is completion-
// only at the worker, which requires a completion binding for every frozen
// requirement of every Level. This test drives the real Access source, the UQ
// client and the worker end to end and proves the Slot completes as
// FULL_EMPTY_COMPLETED with LastFullSlot advanced and without any query failure.
func TestProductionPhaseTwoG4OsRestartEmptyPrimaryWithHistoryDataCompletesFullEmpty(t *testing.T) {
	t.Parallel()
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	const strategyID = int64(4105)
	document := controlledG4StrategyDocument(t, strategyID, strategy.DetectorKindOsRestart, "uptime", "system.env",
		[]string{"bk_target_cloud_id", "bk_target_ip"}, map[string]any{})
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
	var primaryQueries, historyQueries atomic.Int64
	uqClient := &http.Client{Transport: controlledRoundTripper(func(request *http.Request) (*http.Response, error) {
		payload, err := decodeControlledG4UQRequest(request)
		if err != nil {
			return nil, err
		}
		end, err := strconv.ParseInt(payload.EndTime, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("controlled UQ end_time=%q: %w", payload.EndTime, err)
		}
		if payload.MetricMerge == "a <= 3600" {
			primaryQueries.Add(1)
			return controlledG4UQEmptyResponse(request, "system.env")
		}
		historyQueries.Add(1)
		return controlledG4UQResponse(request, "system.env", controlledG4UQSeries(base, end, "system.env", payload.MetricMerge))
	})}

	cfg := controlledG4RuntimeConfig(address, "http://controlled-uq", "alarmd-g4-controlled-empty-primary")
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

	if len(bundle.queryGroups) != 1 {
		t.Fatalf("controlled assigned Query Groups=%v, want one", bundle.queryGroups)
	}
	production, ok := bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
	if !ok {
		t.Fatalf("production ownership type=%T", bundle.dependencies.Ownership)
	}
	progress := loadPhaseTwoProgress(t, ctx, production, bundle.queryGroups[0])
	if progress.LastCompletionKind != execution.CompletionFullEmpty || progress.LastFullSlot != execution.EvaluationTime(base+60) ||
		progress.NextSlot != execution.EvaluationTime(base+120) || progress.CurrentOrRecentGap != nil {
		observationsMu.Lock()
		captured := append([]observability.Observation(nil), observations...)
		observationsMu.Unlock()
		var failures []string
		for _, observation := range captured {
			observation = observability.NormalizeObservation(observation)
			if observation.Stage == observability.StageQueryCompleted && observation.QueryFailure != nil {
				failures = append(failures, fmt.Sprintf("%+v: %v", *observation.QueryFailure, observation.Err))
			}
		}
		t.Fatalf("empty PRIMARY with DATA history Progress=%+v, want FULL_EMPTY_COMPLETED with LastFullSlot advanced; query failures=%v", progress, failures)
	}
	if written := events.snapshot(); len(written) != 0 {
		t.Fatalf("no-series Plan produced events: %v", controlledEventKinds(written))
	}
	if primaryQueries.Load() != 2 || historyQueries.Load() != 2 {
		t.Fatalf("UQ queries primary=%d history=%d, want exactly one of each per Slot without retries", primaryQueries.Load(), historyQueries.Load())
	}

	observationsMu.Lock()
	captured := append([]observability.Observation(nil), observations...)
	observationsMu.Unlock()
	queryCompleted := 0
	for _, observation := range captured {
		observation = observability.NormalizeObservation(observation)
		if observation.Stage != observability.StageQueryCompleted {
			continue
		}
		queryCompleted++
		if observation.Result == observability.ResultFailed || observation.QueryFailure != nil {
			t.Fatalf("query_completed reported a failure for the no-series OsRestart Plan: result=%s failure=%+v error=%v",
				observation.Result, observation.QueryFailure, observation.Err)
		}
	}
	if queryCompleted != 2 {
		t.Fatalf("query_completed observations=%d, want one per Slot", queryCompleted)
	}
	assertObservedOrder(t, observedStages(captured), []observability.Stage{
		observability.StageQueryCompleted, observability.StageProgressCommitted,
		observability.StageQueryCompleted, observability.StageProgressCommitted,
	})
}

func controlledG4UQEmptyResponse(request *http.Request, table string) (*http.Response, error) {
	var encoded bytes.Buffer
	if err := json.NewEncoder(&encoded).Encode(map[string]any{
		"series": []any{}, "status": nil, "trace_id": "g4-controlled-uq", "is_partial": false,
		"result_table_id": []string{table},
	}); err != nil {
		return nil, fmt.Errorf("encode controlled empty UQ response: %w", err)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(bytes.NewReader(encoded.Bytes())), Request: request}, nil
}
