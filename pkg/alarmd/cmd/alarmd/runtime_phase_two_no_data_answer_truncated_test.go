// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The cut travels the production wiring. A grouped event count whose Plan
// detects no-data is answered by the query service, round 0, with exactly
// its terms cap of groups (10000): the query service's client names the
// suspected cut on the query's route, the access layer carries the route
// into the Slot's completion, and the worker files SKIPPED_ANSWER_TRUNCATED
// for the Plan. Round 1 is answered with one group fewer, which is no cut:
// the same Plan is judged, EVALUATED. Each step has a unit test of its own;
// this is the one that fails if any hand-off between them drops the fact.
func TestACutAnswerFromTheQueryServiceReachesTheNoDataOutcome(t *testing.T) {
	const capGroups = 10000
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()

	var document map[string]any
	if err := json.Unmarshal(controlledG4StrategyDocument(t, 7301, "Threshold", "count", "system_event", []string{"host"},
		[]any{[]any{map[string]any{"method": "gte", "threshold": 1_000_000}}}), &document); err != nil {
		t.Fatal(err)
	}
	item := document["items"].([]any)[0].(map[string]any)
	query := item["query_configs"].([]any)[0].(map[string]any)
	query["data_source_label"], query["data_type_label"], query["agg_method"] = "custom", "event", "COUNT"
	query["custom_event_name"] = "synthetic-event"
	item["no_data_config"] = map[string]any{"is_enabled": true, "continuous": 1, "level": 2, "agg_dimension": []any{"host"}}
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "alarm-config.strategy_ids", "[7301]", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "alarm-config.strategy_7301", body, 0).Err(); err != nil {
		t.Fatal(err)
	}

	const period = int64(60)
	base := controlledG4Base(t)
	var clock atomic.Int64
	clock.Store(base)
	var groups atomic.Int64
	uq := &http.Client{Transport: controlledRoundTripper(func(request *http.Request) (*http.Response, error) {
		payload, err := decodeControlledG4UQRequest(request)
		if err != nil {
			return nil, err
		}
		end, err := strconv.ParseInt(payload.EndTime, 10, 64)
		if err != nil {
			return nil, err
		}
		series := make([]any, 0, groups.Load())
		for index := int64(0); index < groups.Load(); index++ {
			series = append(series, map[string]any{"name": "_result0", "columns": []string{"_time", "_value"},
				"types": []string{"float", "float"}, "group_keys": []string{"host"},
				"group_values": []string{"host-" + strconv.FormatInt(index, 10)},
				"values":       []any{[]any{(end - period) * 1000, 1}}})
		}
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(map[string]any{"series": series, "status": nil, "trace_id": "answer-cut", "is_partial": false}); err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(&buf), Request: request}, nil
	})}

	var (
		mu       sync.Mutex
		outcomes = map[string]int{}
		cuts     int
	)
	cfg := controlledG4RuntimeConfig(address, "http://controlled-uq", "alarmd-answer-cut")
	bundle, err := openProductionPhaseTwoBundleWithDependencies(ctx, cfg, metric.NewRecorder(metric.BuildInfo{}),
		observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		}, phaseTwoProductionExternalDependencies{
			Now: func() time.Time { return time.Unix(clock.Load(), 0) }, HTTPClient: uq,
			AdditionalObserver: observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
				mu.Lock()
				defer mu.Unlock()
				if o.NoDataSlot != nil {
					outcomes[o.NoDataSlot.Outcome] += o.NoDataSlot.Plans
				}
				cuts += len(o.QueryTruncation)
			}),
			PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			}),
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bundle.Shutdown(context.Background()) })

	round := func(index int, answered int64) (map[string]int, int) {
		t.Helper()
		groups.Store(answered)
		mu.Lock()
		outcomes, cuts = map[string]int{}, 0
		mu.Unlock()
		clock.Store(base + int64(index)*period + 1)
		if err := runScheduledOnceSettled(ctx, bundle); err != nil {
			t.Fatalf("round %d: %v", index, err)
		}
		mu.Lock()
		defer mu.Unlock()
		copied := make(map[string]int, len(outcomes))
		for outcome, plans := range outcomes {
			copied[outcome] = plans
		}
		return copied, cuts
	}

	got, seen := round(0, capGroups)
	if got["SKIPPED_ANSWER_TRUNCATED"] != 1 || seen == 0 {
		t.Fatalf("round 0, answered with the cap of %d groups: no-data outcomes %v and %d cut queries seen; want "+
			"SKIPPED_ANSWER_TRUNCATED and the cut named on the query", capGroups, got, seen)
	}
	got, seen = round(1, capGroups-1)
	if got["EVALUATED"] != 1 || seen != 0 {
		t.Fatalf("round 1, answered with %d groups: no-data outcomes %v and %d cut queries seen; want EVALUATED and no cut",
			capGroups-1, got, seen)
	}
}
