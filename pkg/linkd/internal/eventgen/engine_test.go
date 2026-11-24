// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package eventgen

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture() Config {
	return Config{TenantID: "tenant-a", Seed: 42, Datasets: map[string]Dataset{"hosts": {Rows: []map[string]any{{"id": 1, "ip": "10.0.0.1", "model": "host"}, {"id": 2, "ip": "10.0.0.2", "model": "host"}}}, "models": {Rows: []map[string]any{{"id": "host", "label": "主机"}}}}, Templates: map[string]Template{"fire": {Kind: "standard", Body: `{"title":{{json (printf "%v CPU high" .Bindings.host.ip)}},"dimensions":{"id":{{json .Bindings.host.id}},"ip":{{json .Bindings.host.ip}}},"values":{"cpu":{{randInt 80 100}}},"evaluations":[{"severity":"warning","action":"triggered"}]}`}, "recover": {Kind: "standard", Body: `{"evaluations":[{"severity":"warning","action":"resolved"}]}`}}, Scenarios: []Scenario{{ID: "cpu", SourceID: "source", Bindings: []Binding{{Name: "host", Dataset: "hosts", Mode: "iterate", Loop: true}, {Name: "model", Dataset: "models", Mode: "random", Where: map[string]string{"id": `{{json .Bindings.host.model}}`}}}, Arrival: Arrival{PerSecond: 1, Count: 1}, Stages: []Stage{{Name: "active", Template: "fire", Duration: Span{Fixed: "2s"}, Every: Span{Fixed: "1s"}}, {Name: "recovered", Template: "recover", Terminal: true}}}}}
}

func startTime() time.Time { return time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC) }

func TestPreviewLifecycleAndStableRows(t *testing.T) {
	c := fixture()
	records, e := Preview(c, startTime(), 10)
	if e != nil {
		t.Fatal(e)
	}
	if len(records) != 3 {
		t.Fatalf("records=%d", len(records))
	}
	for i, r := range records {
		if r.Fingerprint != records[0].Fingerprint || !r.Timestamp.Equal(startTime().Add(time.Duration(i)*time.Second)) {
			t.Fatalf("unstable identity/time %+v", r)
		}
		var body map[string]any
		if e = json.Unmarshal(r.Body, &body); e != nil {
			t.Fatal(e)
		}
		if i < 2 {
			dims := body["dimensions"].(map[string]any)
			if dims["id"] != float64(1) || dims["ip"] != "10.0.0.1" {
				t.Fatal(dims)
			}
		}
	}
	again, e := Preview(c, startTime(), 10)
	if e != nil || !reflect.DeepEqual(records, again) {
		t.Fatal("preview is not reproducible", e)
	}
}

func TestCSVTypedImport(t *testing.T) {
	d, e := ImportCSV("id,enabled,ip\n42,true,10.0.0.1\n", map[string]string{"id": "number", "enabled": "boolean"})
	if e != nil {
		t.Fatal(e)
	}
	if d.Rows[0]["id"] != float64(42) || d.Rows[0]["enabled"] != true {
		t.Fatal(d)
	}
	if _, e = ImportCSV("id\nx", map[string]string{"id": "number"}); e == nil {
		t.Fatal("invalid numeric value accepted")
	}
}

func TestConfigurationFailureAndNoMatch(t *testing.T) {
	c := fixture()
	c.Scenarios[0].Bindings[1].Where["id"] = `"absent"`
	if _, e := Preview(c, startTime(), 1); e == nil {
		t.Fatal("missing data ignored")
	}
	c = fixture()
	c.Scenarios[0].Stages[0].Next = []Branch{{Stage: "unknown", Weight: 1}}
	if _, e := Compile(c); e == nil {
		t.Fatal("invalid stage accepted")
	}
	c = fixture()
	v := c.Templates["fire"]
	v.Kind = "json"
	c.Templates["fire"] = v
	if _, e := Compile(c); e == nil {
		t.Fatal("missing custom cleanup accepted")
	}
}

func TestTraversalStopsAndRandomWeight(t *testing.T) {
	c := fixture()
	c.Scenarios[0].Arrival.Count = 0
	c.Scenarios[0].Bindings[0].Loop = false
	r, e := Preview(c, startTime(), 20)
	if e != nil {
		t.Fatal(e)
	}
	if len(r) != 6 {
		t.Fatalf("one pass generated %d events", len(r))
	}
	c = fixture()
	d := c.Datasets["hosts"]
	d.WeightColumn = "weight"
	for _, row := range d.Rows {
		row["weight"] = 1
	}
	c.Datasets["hosts"] = d
	c.Scenarios[0].Bindings[0].Mode = "weighted"
	if _, e := Preview(c, startTime(), 3); e != nil {
		t.Fatal(e)
	}
}

func TestStageBoundaryAndWeightedLoop(t *testing.T) {
	c := fixture()
	c.Scenarios[0].Stages[0].Next = []Branch{{Stage: "active", Weight: 1}}
	r, e := Preview(c, startTime(), 6)
	if e != nil {
		t.Fatal(e)
	}
	for i, v := range r {
		if !v.Timestamp.Equal(startTime().Add(time.Duration(i) * time.Second)) {
			t.Fatal("duplicated boundary", r)
		}
	}
}

func TestDuplicateFreezesRecord(t *testing.T) {
	c := fixture()
	c.Scenarios[0].DuplicatePercent = 100
	r, e := Preview(c, startTime(), 2)
	if e != nil {
		t.Fatal(e)
	}
	if string(r[0].Body) != string(r[1].Body) || r[0].SourceEventID != r[1].SourceEventID || !reflect.DeepEqual(r[0].Headers, r[1].Headers) {
		t.Fatal("duplicate re-rendered")
	}
}

func testPlan(e *Engine, at time.Time) []Record {
	r := e.plan(at)
	e.ack(sendResult{r, make([]error, len(r))})
	return r
}

func TestPauseFreezesClockAndRateUpdate(t *testing.T) {
	c := fixture()
	e, err := New(c, "test", 1, startTime(), nil)
	if err != nil {
		t.Fatal(err)
	}
	testPlan(e, startTime())
	if err = e.Control("pause"); err != nil {
		t.Fatal(err)
	}
	if r := testPlan(e, startTime().Add(30*time.Second)); len(r) != 0 {
		t.Fatal("paused emitted")
	}
	e.paused = false
	e.status.State = "running"
	r := testPlan(e, startTime().Add(31*time.Second))
	if len(r) != 1 {
		t.Fatalf("resume burst %d", len(r))
	}
	c.Scenarios[0].Arrival.PerSecond = 3
	if err = e.Apply(c, 2); err != nil {
		t.Fatal(err)
	}
	if e.stopping || e.status.Revision != 2 {
		t.Fatal("rate update restarted")
	}
}

func TestGracefulSwitchClosesBeforeNewTemplate(t *testing.T) {
	c := fixture()
	e, err := New(c, "test", 1, startTime(), nil)
	if err != nil {
		t.Fatal(err)
	}
	first := testPlan(e, startTime())
	next := fixture()
	next.Duration = "10s"
	if err = e.Apply(next, 2); err != nil {
		t.Fatal(err)
	}
	closeRecords := testPlan(e, startTime().Add(time.Second))
	if len(closeRecords) != 1 || !closeRecords[0].Cleanup || closeRecords[0].Fingerprint != first[0].Fingerprint {
		t.Fatalf("cleanup=%+v", closeRecords)
	}
	testPlan(e, startTime().Add(2*time.Second))
	if e.status.Revision != 2 {
		t.Fatal(e.status)
	}
	fresh := testPlan(e, startTime().Add(3*time.Second))
	if len(fresh) == 0 || fresh[0].Fingerprint == first[0].Fingerprint {
		t.Fatal("new round reused identity")
	}
}

func TestFailedCleanupCanRetryAndCannotApply(t *testing.T) {
	c := fixture()
	e, _ := New(c, "test", 1, startTime(), nil)
	testPlan(e, startTime())
	next := fixture()
	next.Duration = "10s"
	_ = e.Apply(next, 2)
	records := e.plan(startTime().Add(time.Second))
	e.ack(sendResult{records, []error{errors.New("offline")}})
	e.plan(startTime().Add(2 * time.Second))
	if e.status.State != "cleanup_failed" || e.status.Revision != 1 {
		t.Fatal(e.status)
	}
	if err := e.Control("retry_cleanup"); err != nil {
		t.Fatal(err)
	}
	retry := testPlan(e, startTime().Add(3*time.Second))
	if len(retry) != 1 {
		t.Fatal("no retry")
	}
	testPlan(e, startTime().Add(4*time.Second))
	if e.status.Revision != 2 {
		t.Fatal(e.status)
	}
}

type capturePublisher struct {
	mu      sync.Mutex
	records []Record
	block   <-chan struct{}
}

func (p *capturePublisher) Publish(ctx context.Context, r []Record) []error {
	if p.block != nil {
		select {
		case <-p.block:
		case <-ctx.Done():
			out := make([]error, len(r))
			for i := range out {
				out[i] = ctx.Err()
			}
			return out
		}
	}
	p.mu.Lock()
	p.records = append(p.records, r...)
	p.mu.Unlock()
	return make([]error, len(r))
}

func (p *capturePublisher) Close() {}

func TestCancellationAndControlsDuringBlockedSend(t *testing.T) {
	c := fixture()
	c.Limits.SendTimeout = "20ms"
	c.Limits.CleanupTimeout = "100ms"
	blocked := make(chan struct{})
	pub := &capturePublisher{block: blocked}
	e, err := New(c, "run", 1, time.Now(), pub)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()
	time.Sleep(5 * time.Millisecond)
	if err = e.Control("pause"); err != nil {
		t.Fatal(err)
	}
	if e.Snapshot().State != "paused" {
		t.Fatal("control blocked")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancel leaked runtime")
	}
}

func BenchmarkTemplateGeneration(b *testing.B) {
	c := fixture()
	c.Scenarios[0].Arrival.Count = 0
	p, e := Compile(c)
	if e != nil {
		b.Fatal(e)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ctx, e := p.instance(0, "bench", uint64(i), startTime())
		if e != nil {
			b.Fatal(e)
		}
		if _, e = p.render(0, 0, ctx, 1, startTime(), false); e != nil {
			b.Fatal(e)
		}
	}
}

func TestRunContinuesAfterEmptySchedulingTick(t *testing.T) {
	c := fixture()
	c.Scenarios[0].Stages[0].Duration = Span{Fixed: "30ms"}
	c.Scenarios[0].Stages[0].Every = Span{Fixed: "10ms"}
	pub := &capturePublisher{}
	engine, err := New(c, "run", 1, time.Now(), pub)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err = engine.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if engine.Snapshot().State != "completed" || len(pub.records) != 4 {
		t.Fatalf("runtime stalled: %+v records=%d", engine.Snapshot(), len(pub.records))
	}
}

func TestDelayedMessagesObeyByteAndCountBudgets(t *testing.T) {
	c := fixture()
	c.Scenarios[0].Arrival = Arrival{PerSecond: 10000}
	c.Scenarios[0].SendDelay = Span{Fixed: "10s"}
	c.Limits = Limits{QueueRecords: 8, QueueBytes: 8192, MaxMessageBytes: 1024, Batch: 4}
	e, err := New(c, "test", 1, startTime(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		testPlan(e, startTime().Add(time.Duration(i)*time.Millisecond))
	}
	s := e.Snapshot()
	if s.QueueBytes > 8192 || s.Queued > 8 || s.Queued == 0 {
		t.Fatal(s)
	}
}

func TestCleanupRetryKeepsOriginalDelivery(t *testing.T) {
	c := fixture()
	e, _ := New(c, "test", 1, startTime(), nil)
	testPlan(e, startTime())
	_ = e.Control("stop")
	records := e.plan(startTime().Add(time.Second))
	e.ack(sendResult{records, []error{errors.New("offline")}})
	e.plan(startTime().Add(2 * time.Second))
	_ = e.Control("retry_cleanup")
	retry := e.plan(startTime().Add(3 * time.Second))
	if len(retry) != 1 || string(retry[0].Body) != string(records[0].Body) || retry[0].SourceEventID != records[0].SourceEventID {
		t.Fatal("cleanup retry changed delivery")
	}
}

func TestStopBeforeScheduledStartAndDelayedDrain(t *testing.T) {
	c := fixture()
	e, err := New(c, "test", 1, startTime().Add(time.Hour), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Control("stop"); err != nil {
		t.Fatal(err)
	}
	testPlan(e, startTime())
	if e.Snapshot().State != "completed" {
		t.Fatal("stop waited for future start")
	}
	c.Scenarios[0].SendDelay = Span{Fixed: "1h"}
	e, _ = New(c, "test", 1, startTime(), nil)
	testPlan(e, startTime())
	_ = e.Control("stop")
	records := testPlan(e, startTime().Add(time.Second))
	if len(records) == 0 {
		t.Fatal("shutdown waited for artificial delivery delay")
	}
	testPlan(e, startTime().Add(2*time.Second))
	testPlan(e, startTime().Add(3*time.Second))
	if e.Snapshot().State != "completed" {
		t.Fatal(e.Snapshot())
	}
}

func TestLargeObjectIdentityRemainsExact(t *testing.T) {
	c := fixture()
	c.Datasets["hosts"].Rows[0]["id"] = json.Number("9007199254740993")
	records, err := Preview(c, startTime(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(records[0].Body), `"id":9007199254740993`) {
		t.Fatalf("object ID rounded: %s", records[0].Body)
	}
}
