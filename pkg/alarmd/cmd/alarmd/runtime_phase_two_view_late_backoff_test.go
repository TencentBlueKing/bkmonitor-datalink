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
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// A Worker that starts before its view arrives refuses each of its Query
// Groups once and backs off. When the view comes, a Query Group whose next
// Slot is minutes or an hour away is read once, found not due, and left until
// that Slot. It used to keep the backoff's ready time, already past, and the
// dispatcher queued it again on every return: after each rollout a Worker
// re-read its long-period Query Groups' schedules hundreds of times a second
// until their next Slot ran, at most an hour.
func TestAWorkerWhoseViewArrivesLateReadsEachLongPeriodGroupOnceUntilItsSlot(t *testing.T) {
	const groups = 12
	address, client := startPhaseTwoRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ids := make([]int, 0, groups)
	pipe := client.TxPipeline()
	for id := 1; id <= groups; id++ {
		ids = append(ids, id)
		// Half on 300 s and half on an hour: neither has a Slot due in this
		// test unless the run happens to straddle its grid point.
		period := int64(300)
		if id%2 == 0 {
			period = 3600
		}
		pipe.Set(ctx, "alarm-config.strategy_"+strconv.Itoa(id), viewLateStrategyDocument(t, int64(id), period), 0)
	}
	encoded, _ := json.Marshal(ids)
	pipe.Set(ctx, "alarm-config.strategy_ids", string(encoded), 0)
	pipe.Set(ctx, "alarm-config.last_updated", strconv.FormatInt(time.Now().Unix(), 10), 0)
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}

	recorder := metric.NewRecorder(metric.BuildInfo{})
	cfg := controlledG4RuntimeConfig(address, "http://view-late-uq", "alarmd-view-late")
	bundle, err := openProductionPhaseTwoBundleWithDependencies(ctx, cfg, recorder,
		observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		}, phaseTwoProductionExternalDependencies{
			Now: time.Now, HTTPClient: &http.Client{Transport: controlledRoundTripper(viewLateAnswer)},
			PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			}),
		})
	if err != nil {
		t.Fatal(err)
	}
	ran := make(chan error, 1)
	go func() { ran <- bundle.Run(ctx) }()
	// No view yet: every round is refused and backs off.
	deadline := time.Now().Add(30 * time.Second)
	for refusals(t, recorder) < groups && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if refused := refusals(t, recorder); refused < groups {
		t.Fatalf("%v rounds refused for want of a view before it was served, want each of the %d Query Groups", refused, groups)
	}
	serveControlStreamForTest(bundle)
	// Past the longest backoff the refusals can have grown to, with the view
	// in place for all of it.
	time.Sleep(12 * time.Second)
	cancel()
	<-ran

	// Each Query Group is found not due once when its backoff ends, and the
	// walk confirms that once more after a Slot it straddles. A Runner left
	// ready runs thousands of rounds in this time.
	misses := counterValue(t, recorder, "bkmonitor_alarmd_due_index_prediction_total",
		map[string]string{"prediction": "due", "actual": "not_due"})
	if misses > 3*groups {
		t.Fatalf("%v rounds were dispatched as due and found not due over %d long-period Query Groups, want at most %d: "+
			"a Runner whose source backoff outlived a successful read is queued again on every return", misses, groups, 3*groups)
	}
}

func refusals(t *testing.T, recorder *metric.Recorder) float64 {
	t.Helper()
	return counterValue(t, recorder, "bkmonitor_alarmd_due_index_recomputed_total", map[string]string{"trigger": "deferral"})
}

// viewLateStrategyDocument is one threshold strategy on its own table, so
// each is its own Query Group, at the period given.
func viewLateStrategyDocument(t *testing.T, id, period int64) string {
	t.Helper()
	document := controlledG4StrategyDocument(t, id, strategy.DetectorKindThreshold, "usage",
		fmt.Sprintf("view_late.p%d_%d", period, id), []string{"host"},
		[]any{[]any{map[string]any{"method": "gte", "threshold": 1_000_000}}})
	var decoded map[string]any
	if err := json.Unmarshal(document, &decoded); err != nil {
		t.Fatal(err)
	}
	decoded["items"].([]any)[0].(map[string]any)["query_configs"].([]any)[0].(map[string]any)["agg_interval"] = period
	encoded, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// viewLateAnswer answers any query at once with one point at its end.
func viewLateAnswer(request *http.Request) (*http.Response, error) {
	payload, err := decodeControlledG4UQRequest(request)
	if err != nil {
		return nil, err
	}
	table := ""
	if len(payload.QueryList) > 0 {
		table = payload.QueryList[0].TableID
	}
	end, _ := strconv.ParseInt(payload.EndTime, 10, 64)
	series := map[string]any{"name": "_result0", "columns": []string{"_time", "_result"}, "types": []string{"int64", "float64"},
		"group_keys": []string{"host"}, "group_values": []string{"view-late-host"}, "values": []any{[]any{end*1000 - 1000, 1.0}}}
	var encoded bytes.Buffer
	if err := json.NewEncoder(&encoded).Encode(map[string]any{"series": []any{series}, "status": nil, "trace_id": "view-late",
		"is_partial": false, "result_table_id": []string{table}}); err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(bytes.NewReader(encoded.Bytes())), Request: request}, nil
}
