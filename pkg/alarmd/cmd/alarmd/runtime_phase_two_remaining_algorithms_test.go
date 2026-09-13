package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Exercise the production source/compiler/query/worker/state path, using only
// Redis and an in-process UQ transport. The independent Python oracle covers
// arithmetic; this test catches algorithms that work in isolation but cannot
// reach an event or falsely recover when a historical query fails.
func TestProductionRemainingAlgorithms(t *testing.T) {
	tests := []struct {
		kind    string
		config  map[string]any
		offsets []int64
		current float64
		normal  float64
	}{
		{"SimpleYearRound", map[string]any{"floor": 50, "ceil": nil}, []int64{604800}, 40, 100},
		{"AdvancedRingRatio", map[string]any{"floor": 50, "ceil": nil, "floor_interval": 2, "ceil_interval": nil, "fetch_type": "avg"}, []int64{60, 120}, 40, 100},
		{"AdvancedYearRound", map[string]any{"floor": 50, "ceil": nil, "floor_interval": 2, "ceil_interval": nil, "fetch_type": "avg"}, []int64{86400, 172800}, 40, 100},
		{"RingRatioAmplitude", map[string]any{"ratio": 0.5, "shock": 1, "threshold": 10}, []int64{60}, 200, 200},
		{"YearRoundAmplitude", map[string]any{"ratio": 1, "shock": 50, "days": 2, "method": "gt"}, []int64{60, 86400, 86460, 172800, 172860}, 200, 200},
		{"YearRoundRange", map[string]any{"ratio": 1.5, "shock": 0, "days": 2, "method": "gt"}, []int64{86400, 172800}, 200, 100},
	}
	for _, test := range tests {
		for _, historyState := range []string{"available", "unavailable", "empty"} {
			t.Run(fmt.Sprintf("%s/history=%s", test.kind, historyState), func(t *testing.T) {
				address, client := startPhaseTwoRedis(t)
				ctx := context.Background()
				document := controlledG4StrategyDocument(t, 7001, test.kind, "usage", "system.cpu", []string{"host"}, test.config)
				if err := client.Set(ctx, "alarm-config.strategy_ids", "[7001]", 0).Err(); err != nil {
					t.Fatal(err)
				}
				if err := client.Set(ctx, "alarm-config.strategy_7001", string(document), 0).Err(); err != nil {
					t.Fatal(err)
				}
				base := controlledG4Base(t)
				var clock atomic.Int64
				clock.Store(base)
				points := map[int64]float64{}
				for _, slot := range []int64{base, base + 60} {
					for _, offset := range test.offsets {
						points[slot-1-offset] = 100
					}
				}
				points[base-1], points[base+59] = test.current, test.normal
				var mu sync.Mutex
				var observations []observability.Observation
				var queriedHistory bool
				uq := &http.Client{Transport: controlledRoundTripper(func(request *http.Request) (*http.Response, error) {
					payload, err := decodeControlledG4UQRequest(request)
					if err != nil {
						return nil, err
					}
					start, err := strconv.ParseInt(payload.StartTime, 10, 64)
					if err != nil {
						return nil, err
					}
					end, err := strconv.ParseInt(payload.EndTime, 10, 64)
					if err != nil {
						return nil, err
					}
					// A few daily points must not fetch a week of continuous data.
					if end-start > 180 {
						return nil, fmt.Errorf("unbounded historical query [%d,%d)", start, end)
					}
					isHistory := end < clock.Load()-1
					failHistory := isHistory && clock.Load() > base+1
					if isHistory {
						mu.Lock()
						queriedHistory = true
						mu.Unlock()
						if failHistory && historyState == "unavailable" {
							return &http.Response{StatusCode: http.StatusBadGateway, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("history unavailable")), Request: request}, nil
						}
					}
					times := make([]int64, 0, len(points))
					for timestamp := range points {
						if timestamp >= start && timestamp < end && !(failHistory && historyState == "empty") {
							times = append(times, timestamp)
						}
					}
					sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
					values := make([]any, 0, len(times))
					for _, timestamp := range times {
						values = append(values, []any{timestamp * 1000, points[timestamp]})
					}
					return controlledG4UQResponse(request, "system.cpu", map[string]any{
						"name": "_result0", "columns": []string{"_time", "_result"}, "types": []string{"int64", "float64"},
						"group_keys": []string{"host"}, "group_values": []string{"controlled-host"}, "values": values,
					})
				})}
				cfg := controlledG4RuntimeConfig(address, "http://controlled-uq", "alarmd-remaining-controlled")
				events := &recordingPhaseTwoEventSink{}
				bundle, err := openProductionPhaseTwoBundleWithDependencies(ctx, cfg,
					metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
					func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
						return controlplane.NewLegacyRedisStrategySource(client, prefix)
					}, phaseTwoProductionExternalDependencies{
						Now: func() time.Time { return time.Unix(clock.Load(), 0) }, HTTPClient: uq,
						AdditionalObserver: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
							mu.Lock()
							defer mu.Unlock()
							observations = append(observations, observability.NormalizeObservation(observation))
						}),
						OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) { return events, nil },
					})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := bundle.Shutdown(ctx); err != nil {
						t.Error(err)
					}
				})
				if err := bundle.Start(ctx); err != nil {
					t.Fatal(err)
				}
				for _, slot := range []int64{base, base + 60} {
					clock.Store(slot + 1)
					if err := bundle.runScheduledOnce(ctx); err != nil {
						t.Fatal(err)
					}
				}
				written := events.snapshot()
				mu.Lock()
				captured := append([]observability.Observation(nil), observations...)
				queried := queriedHistory
				mu.Unlock()
				if !queried || len(bundle.queryGroups) != 1 {
					t.Fatalf("history queried=%t, groups=%d", queried, len(bundle.queryGroups))
				}
				if historyState != "available" {
					if len(written) != 1 || written[0].EventKind != contract.TriggerEventAbnormal {
						t.Fatalf("%s history must preserve the first anomaly without recovery: %+v", historyState, written)
					}
					unknown := false
					for _, observation := range captured {
						for _, fact := range observation.AlgorithmEvaluations {
							unknown = unknown || string(fact.DetectorKind) == test.kind && fact.Result == observability.AlgorithmEvaluationResultUnavailable
						}
					}
					if !unknown {
						var stages []string
						for _, observation := range captured {
							stages = append(stages, fmt.Sprintf("%s/%s/%s/%v/evaluations=%v", observation.Stage, observation.Result, observation.ReasonCode, observation.Err, observation.AlgorithmEvaluations))
						}
						t.Fatalf("%s history did not record an unavailable algorithm outcome: %v", historyState, stages)
					}
					return
				}
				if len(written) != 2 || written[0].EventKind != contract.TriggerEventAbnormal || written[1].EventKind != contract.TriggerEventRecovery {
					encoded, _ := json.Marshal(captured)
					t.Fatalf("events=%v; observations=%s", controlledEventKinds(written), encoded)
				}
				progress := loadPhaseTwoProgress(t, ctx, bundle.dependencies.Ownership.(*productionPhaseTwoOwnership), bundle.queryGroups[0])
				if progress.LastFullSlot != execution.EvaluationTime(base+60) || progress.NextSlot != execution.EvaluationTime(base+120) {
					t.Fatalf("event completion did not commit progress: %+v", progress)
				}
				seen := map[observability.AlgorithmEvaluationResult]bool{}
				for _, observation := range captured {
					for _, fact := range observation.AlgorithmEvaluations {
						if string(fact.DetectorKind) == test.kind {
							seen[fact.Result] = true
						}
					}
				}
				if !seen[observability.AlgorithmEvaluationResultAbnormal] || !seen[observability.AlgorithmEvaluationResultRecovery] {
					t.Fatalf("algorithm observations lost new family: %v", seen)
				}
				assertObservedOrder(t, observedStages(captured), []observability.Stage{
					observability.StageEvaluationCompleted, observability.StageEventACKed, observability.StageStateApplied, observability.StageProgressCommitted,
				})
			})
		}
	}
}
