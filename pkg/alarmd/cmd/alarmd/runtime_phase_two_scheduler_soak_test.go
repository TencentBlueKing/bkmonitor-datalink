// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build soak

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// The scheduler shape, the read stagger and the read hold are controllers
// over many Query Groups at once, and a one-group harness on a stepped clock
// cannot show how they behave together: the floors serve one group, and a
// clock stepped per request makes every query sequential. This runs the
// production bundle on the wall clock and a real redis-server, over groups
// on three periods, against a query service with realistic latency, through
// a cold start and a step of demand up and back down, and holds it to what a
// deployment needs of them.
//
// It takes minutes and runs only with -tags soak: in the merge gate of any
// change to the scheduler shape, the stagger or the read hold.
func TestSoakTheSchedulerShapeStaggerAndHoldTogether(t *testing.T) {
	const (
		// Enough groups that the floors (2 permits, 4 execution slots) cannot
		// serve them: at 40 a self-throttled shape still kept up, and the
		// soak passed on a build whose budget throttled itself in a deployment.
		baseGroups = 200
		stepGroups = 100
		stepUpAt   = 2 * time.Minute
		stepDownAt = 4 * time.Minute
	)
	runFor := 5*time.Minute + 30*time.Second
	if raw := os.Getenv("ALARMD_SOAK_SECONDS"); raw != "" {
		seconds, _ := strconv.Atoi(raw)
		runFor = time.Duration(seconds) * time.Second
	}
	address, client := startPhaseTwoRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Periods: a fifth on 10 s, three fifths on 60 s, a fifth on 300 s.
	period := func(id int) int64 {
		switch id % 5 {
		case 0:
			return 10
		case 4:
			return 300
		default:
			return 60
		}
	}
	install := func(ids []int) {
		t.Helper()
		all := []int{}
		for id := 1; id <= baseGroups+stepGroups; id++ {
			if contains(ids, id) {
				all = append(all, id)
			}
		}
		encoded, _ := json.Marshal(all)
		pipe := client.TxPipeline()
		pipe.Set(ctx, "alarm-config.strategy_ids", string(encoded), 0)
		for _, id := range all {
			pipe.Set(ctx, "alarm-config.strategy_"+strconv.Itoa(id), soakStrategyDocument(t, int64(id), period(id)), 0)
		}
		pipe.Set(ctx, "alarm-config.last_updated", strconv.FormatInt(time.Now().Unix(), 10), 0)
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	base := make([]int, 0, baseGroups)
	for id := 1; id <= baseGroups; id++ {
		base = append(base, id)
	}
	install(base)

	// The query service: 50-200 ms, one answer in five taking 3 s.
	backend := &soakBackend{random: rand.New(rand.NewSource(1))}
	recorder := metric.NewRecorder(metric.BuildInfo{})
	var mu sync.Mutex
	entered := 0
	outcomes := map[string]int{}
	cfg := controlledG4RuntimeConfig(address, "http://soak-uq", "alarmd-soak")
	cfg.PhaseTwo.Access.MinReadyDelay = config.Duration(30 * time.Second)
	cfg.PhaseTwo.Access.DownstreamExecutionReserve = config.Duration(5 * time.Second)
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(2 * time.Second)
	bundle, err := openProductionPhaseTwoBundleWithDependencies(ctx, cfg, recorder,
		observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		}, phaseTwoProductionExternalDependencies{
			Now: time.Now, HTTPClient: &http.Client{Transport: controlledRoundTripper(backend.roundTrip)},
			AdditionalObserver: observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
				mu.Lock()
				defer mu.Unlock()
				if o.QueryCooldown != nil && o.QueryCooldown.Event == "entered" {
					entered++
				}
				outcomes[string(o.Component)+"/"+string(o.Stage)+"/"+string(o.Result)+"/"+string(o.ReasonCode)]++
			}),
			PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			}),
		})
	if err != nil {
		t.Fatal(err)
	}
	// The control stream is served by the process's HTTP server in
	// production, not by the bundle; the view never installs without it.
	serveControlStreamForTest(bundle)
	ran := make(chan error, 1)
	go func() { ran <- bundle.Run(ctx) }()

	// Every half second: the shape in force against what is held and waiting.
	type sample struct {
		at                     time.Time
		permits, held, waiting int
		executions             int
	}
	var samples []sample
	started := time.Now()
	stepped, unstepped := false, false
	for time.Since(started) < runFor {
		time.Sleep(500 * time.Millisecond)
		switch {
		case !stepped && time.Since(started) >= stepUpAt:
			stepped = true
			install(append(append([]int{}, base...), soakRange(baseGroups+1, baseGroups+stepGroups)...))
		case stepped && !unstepped && time.Since(started) >= stepDownAt:
			unstepped = true
			install(base)
		}
		occupancy := bundle.shaper.flights.QueryPermitOccupancy()
		held := occupancy.LookbackInflight
		for _, count := range occupancy.Inflight {
			held += count
		}
		shape := bundle.schedulerShape()
		samples = append(samples, sample{at: time.Now(), permits: shape.ProcessQueryPermits, held: held,
			waiting: occupancy.Waiting["normal"], executions: shape.ActiveExecutions})
	}
	cancel()
	<-ran
	shapes := map[[2]int]int{}
	for _, each := range samples {
		shapes[[2]int{each.permits, each.executions}]++
	}
	t.Logf("soak: half-second samples by (permits, execution slots) in force: %v", shapes)

	// Every bound at or above what it serves: no sample holds more permits
	// than both the budget in force and the one before it (a lowered budget
	// takes nothing back, so the held may briefly exceed the new one).
	for index := 1; index < len(samples); index++ {
		if limit := max(samples[index].permits, samples[index-1].permits); samples[index].held > limit {
			t.Fatalf("at %s %d permits held against a budget of %d", samples[index].at.Format(time.TimeOnly), samples[index].held, limit)
		}
	}
	// None stuck at its floor while demand exists: a derivation that read
	// queries asking derived more than the floor, and the derivations after
	// the first minute read queries at all.
	readings := bundle.shaper.facts()
	if readings == nil {
		t.Fatal("no derivation in the whole run")
	}
	backend.mu.Lock()
	requests := append([]soakRequest(nil), backend.requests...)
	backend.mu.Unlock()
	if readings.PeakQueriesPerSecond == 0 || (readings.PeakQueriesPerSecond*float64(readings.HoldMillis)/1000*2 > 2 && readings.ProcessQueryPermits <= 2) {
		mu.Lock()
		t.Fatalf("the last derivation read %+v with %d queries made; Slots %v", readings, len(requests), outcomes)
	}
	// No Query Group entered the query cooldown pool, and no query waited
	// past its deadline for a permit: nothing was starved by the budget.
	mu.Lock()
	pooled := entered
	mu.Unlock()
	if pooled != 0 {
		t.Fatalf("%d Query Groups entered the query cooldown pool", pooled)
	}
	if timeouts := soakCounter(t, recorder, "bkmonitor_alarmd_worker_query_admission_total", "result", "timeout"); timeouts != 0 {
		t.Fatalf("%v queries timed out waiting for a permit", timeouts)
	}
	// The step was served: the groups added at its start were read while
	// they were owned (a 300 s group may not come due inside the step).
	if !stepped {
		t.Logf("soak shorter than the step; the step is not checked")
	} else {
		read := map[int64]bool{}
		for _, request := range requests {
			if request.id > baseGroups && request.at.Before(started.Add(stepDownAt)) {
				read[request.id] = true
			}
		}
		for id := int64(baseGroups + 1); id <= baseGroups+stepGroups; id++ {
			if period(int(id)) < 300 && !read[id] {
				t.Fatalf("Query Group %d, added by the step, was never read during it; read %v", id, read)
			}
		}
	}
	// Every group kept its cadence: no two consecutive reads of a 10 s or
	// 60 s group further apart than its period and five seconds, once the
	// first reads are out of the way. A budget that throttled itself shows
	// here first, as a backlog that pushes every read later.
	last := map[int64]time.Time{}
	var late []int64
	worst, worstID, worstGap := 0.0, int64(0), time.Duration(0)
	for _, request := range requests {
		previous, seen := last[request.id]
		last[request.id] = request.at
		if !seen || request.period >= 300 || previous.Before(started.Add(90*time.Second)) {
			continue
		}
		gap := request.at.Sub(previous)
		if over := gap.Seconds() / float64(request.period); over > worst {
			worst, worstID, worstGap = over, request.id, gap
		}
		if gap > time.Duration(request.period)*time.Second+5*time.Second {
			late = append(late, request.id)
		}
	}
	for _, id := range late {
		var at []string
		for _, request := range requests {
			if request.id == id {
				at = append(at, fmt.Sprintf("%.1f", request.at.Sub(started).Seconds()))
			}
		}
		t.Errorf("Query Group %d (every %d s) fell behind its period; read at %v s", id, period(int(id)), at)
	}
	t.Logf("soak: widest gap between reads %s on Query Group %d", worstGap, worstID)
	// The reads spread by phase: one-minute groups read across many seconds
	// of their minute, not in one.
	seconds := map[int64]int{}
	oneMinute := 0
	for _, request := range requests {
		if request.period != 60 {
			continue
		}
		oneMinute++
		seconds[request.at.Unix()%60]++
	}
	most := 0
	for _, count := range seconds {
		most = max(most, count)
	}
	if oneMinute == 0 || len(seconds) < 6 || most*3 > oneMinute {
		t.Fatalf("%d one-minute reads in %d distinct seconds, the busiest holding %d", oneMinute, len(seconds), most)
	}
	mu.Lock()
	t.Logf("soak outcomes: %v", outcomes)
	mu.Unlock()
	t.Logf("soak: %d queries, %d samples, last shape %+v, one-minute reads over %d seconds (busiest %d of %d)",
		len(requests), len(samples), *readings, len(seconds), most, oneMinute)
}

type soakRequest struct {
	at         time.Time
	period, id int64
}

// soakBackend answers each query after 50-200 ms, or 3 s one time in
// five, on the wall clock, and keeps when each arrived.
type soakBackend struct {
	mu       sync.Mutex
	random   *rand.Rand
	requests []soakRequest
}

func (backend *soakBackend) roundTrip(request *http.Request) (*http.Response, error) {
	payload, err := decodeControlledG4UQRequest(request)
	if err != nil {
		return nil, err
	}
	table := ""
	if len(payload.QueryList) > 0 {
		table = payload.QueryList[0].TableID
	}
	var periodSeconds, id int64
	_, _ = fmt.Sscanf(table, "soak.p%d_%d", &periodSeconds, &id)
	backend.mu.Lock()
	backend.requests = append(backend.requests, soakRequest{at: time.Now(), period: periodSeconds, id: id})
	latency := time.Duration(50+backend.random.Intn(150)) * time.Millisecond
	if backend.random.Intn(5) == 0 {
		latency = 3 * time.Second
	}
	backend.mu.Unlock()
	select {
	case <-time.After(latency):
	case <-request.Context().Done():
		return nil, request.Context().Err()
	}
	end, _ := strconv.ParseInt(payload.EndTime, 10, 64)
	series := map[string]any{"name": "_result0", "columns": []string{"_time", "_result"}, "types": []string{"int64", "float64"},
		"group_keys": []string{"host"}, "group_values": []string{"soak-host"}, "values": []any{[]any{end*1000 - 1000, 1.0}}}
	var encoded bytes.Buffer
	if err := json.NewEncoder(&encoded).Encode(map[string]any{"series": []any{series}, "status": nil, "trace_id": "soak",
		"is_partial": false, "result_table_id": []string{table}}); err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(bytes.NewReader(encoded.Bytes())), Request: request}, nil
}

// soakStrategyDocument is one threshold strategy on its own table, so each
// is its own Query Group, at the period given.
func soakStrategyDocument(t *testing.T, id, period int64) string {
	t.Helper()
	table := fmt.Sprintf("soak.p%d_%d", period, id)
	document := controlledG4StrategyDocument(t, id, strategy.DetectorKindThreshold, "usage", table, []string{"host"},
		[]any{[]any{map[string]any{"method": "gte", "threshold": 1_000_000}}})
	var decoded map[string]any
	if err := json.Unmarshal(document, &decoded); err != nil {
		t.Fatal(err)
	}
	item := decoded["items"].([]any)[0].(map[string]any)
	item["query_configs"].([]any)[0].(map[string]any)["agg_interval"] = period
	encoded, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func soakRange(from, to int) []int {
	ids := make([]int, 0, to-from+1)
	for id := from; id <= to; id++ {
		ids = append(ids, id)
	}
	return ids
}

func contains(ids []int, id int) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

// soakCounter is one labelled series of a counter family, summed.
func soakCounter(t *testing.T, recorder *metric.Recorder, family, label, value string) float64 {
	t.Helper()
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	total := 0.0
	for _, f := range families {
		if f.GetName() != family {
			continue
		}
		for _, series := range f.GetMetric() {
			for _, l := range series.GetLabel() {
				if l.GetName() == label && l.GetValue() == value {
					total += series.GetCounter().GetValue()
				}
			}
		}
	}
	return total
}
