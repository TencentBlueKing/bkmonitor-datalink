// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package openalerts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
)

// Every Console method refuses a context without a deadline before it sends
// anything: the client has no timeout of its own, so a caller that forgot
// one would wait forever. The refusal is not counted as a Console failure.
func TestEveryConsoleMethodRefusesACallWithoutADeadline(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	t.Cleanup(server.Close)
	reader := classReader(t, server.URL, server.Client(), nil)
	unbounded := context.Background()
	calls := map[string]func() error{
		"Binding":     func() error { _, err := reader.Binding(unbounded); return err },
		"Reconcile":   func() error { _, err := reader.Reconcile(unbounded, keyA); return err },
		"Roster":      func() error { _, err := reader.Roster(unbounded, ""); return err },
		"AlertRecord": func() error { _, err := reader.AlertRecord(unbounded, "t", "a1"); return err },
		"EventSource": func() error { _, err := reader.EventSource(unbounded); return err },
		"Entities":    func() error { _, err := reader.Entities(unbounded, EntityAlerts, nil); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, ErrNoDeadline) {
			t.Errorf("%s without a deadline: %v, want ErrNoDeadline", name, err)
		}
	}
	reader.Refresh(unbounded)
	if n := requests.Load(); n != 0 {
		t.Fatalf("%d requests sent for calls without a deadline", n)
	}
	for op, call := range reader.Record().Calls {
		if call.Calls != 0 || call.Failures != 0 {
			t.Errorf("%s counted %+v for a refused call", op, call)
		}
	}
}

// A request's deadline comes from the Console's own requests: before eight
// are timed it is the coverage of the work; after, four times their p90, never
// more than the coverage; requests cut at a deadline count at what they took,
// so the deadline can rise past one that cut them.
func TestTheConsoleDeadlineIsDerivedFromItsOwnRequests(t *testing.T) {
	latency := &ConsoleLatency{}
	for index := 0; index < 7; index++ {
		latency.Observe(time.Second)
	}
	if deadline := latency.Deadline(30 * time.Minute); deadline != 30*time.Minute {
		t.Fatalf("seven requests timed: %v, want the 30-minute coverage", deadline)
	}
	latency = &ConsoleLatency{}
	for index := 0; index < 10; index++ {
		latency.Observe(time.Duration(index+1) * 200 * time.Millisecond)
	}
	// Ten requests of 0.2..2.0 s: the p90 is the ninth, 1.8 s.
	if deadline := latency.Deadline(30 * time.Minute); deadline != 4*1800*time.Millisecond {
		t.Fatalf("deadline %v, want four times the 1.8 s p90", deadline)
	}
	for index := 0; index < consoleLatencySamples; index++ {
		latency.Observe(7200 * time.Millisecond)
	}
	if deadline := latency.Deadline(30 * time.Minute); deadline != 4*7200*time.Millisecond {
		t.Fatalf("after requests cut at 7.2 s: deadline %v, want it risen to 28.8 s", deadline)
	}
	if deadline := latency.Deadline(10 * time.Second); deadline != 10*time.Second {
		t.Fatalf("deadline %v past a 10 s coverage, want the coverage", deadline)
	}
}

// Each request of a call is bounded on its own: a reconciliation whose keying
// read and reconcile request go to a Console that never answers ends after
// two request deadlines, not at the call's, and both cut requests are timed.
// The target is read first under a plain deadline and reused by the
// reconciliation, so the 40 ms bound falls on the hung requests alone and not
// on how fast a loaded test server answers the read.
func TestEachConsoleRequestHasItsOwnDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == testLinkdAPIRoot+"/strategy-index/targets" {
			_ = json.NewEncoder(w).Encode([]TargetBinding{testBinding()})
			return
		}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	reader := classReader(t, server.URL, server.Client(), nil)
	for index := 0; index < consoleLatencyColdStart; index++ {
		reader.Latency().Observe(10 * time.Millisecond)
	}
	ctx, cancel := context.WithCancel(testwait.Context(t))
	defer cancel()
	if _, err := reader.Binding(ctx); err != nil {
		t.Fatalf("target read: %v", err)
	}
	started := time.Now()
	_, err := reader.Reconcile(WithRequestCoverage(ctx, time.Minute), keyA)
	if took := time.Since(started); err == nil || took > 5*time.Second {
		t.Fatalf("err %v after %v, want a request deadline of about 40 ms each", err, took)
	}
	if ConsoleFailureClass(err) != execution.TransportFailureTimeout {
		t.Fatalf("class %s, want timeout", ConsoleFailureClass(err))
	}
	if samples := len(reader.Latency().samples); samples != consoleLatencyColdStart+3 {
		t.Fatalf("%d requests timed, want the warm ones, the target read and the two cut requests", samples)
	}
}

// A Console that does not answer holds only the calibration round: the
// refresh round runs meanwhile, and a calibration due while one is still
// running is skipped and counted, not stacked behind it. The hung call ends
// at its deadline, and the round is free again.
func TestAHungConsoleHoldsOnlyItsOwnRound(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	options.ReconcileInterval, options.CalibrationMaxAge = 200*time.Millisecond, 400*time.Millisecond
	entered := make(chan struct{}, 1)
	options.Reconciler = reconcilerFunc(func(ctx context.Context, _ StrategyKey) (Reconciliation, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return Reconciliation{}, ctx.Err()
	})
	cache := mustIndex(t, options)
	if err := cache.SetTracked([]StrategyKey{keyA, keyB}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); cache.calibrate(context.Background()) }()
	<-entered
	refreshed := make(chan struct{})
	go func() { defer close(refreshed); cache.refreshIndex(context.Background()) }()
	select {
	case <-refreshed:
	case <-testwait.Expired(t):
		t.Fatal("the refresh round waited on a hung calibration")
	}
	cache.calibrate(context.Background())
	cache.calibrate(context.Background())
	if skipped := cache.Stats().RoundsSkipped; skipped != 2 {
		t.Fatalf("rounds skipped %d while one ran, want 2", skipped)
	}
	select {
	case <-done:
	case <-testwait.Expired(t):
		t.Fatal("a hung calibration outlived its deadline")
	}
	if !cache.index.calibrateMu.TryLock() {
		t.Fatal("the calibration round is still held after its calls ended")
	}
	cache.index.calibrateMu.Unlock()
}

// A calibration cut by its deadline is counted in the Console record as a
// timeout, the class fleet.get shows: a Console that really stops answering
// is still seen as one.
func TestACutCalibrationIsCountedAsATimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	t.Cleanup(server.Close)
	reader := classReader(t, server.URL, server.Client(), nil)
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	options.ReconcileInterval, options.CalibrationMaxAge, options.ReconcileBatch = 200*time.Millisecond, 400*time.Millisecond, 1
	options.Reconciler = reader
	cache := mustIndex(t, options)
	if err := cache.SetTracked([]StrategyKey{keyA}); err != nil {
		t.Fatal(err)
	}
	cache.calibrate(context.Background())
	call := reader.Record().Calls[ConsoleOpReconcile]
	if call.Failures == 0 || call.LastFailureClass != execution.TransportFailureTimeout {
		t.Fatalf("reconcile record %+v, want a failure classed %s", call, execution.TransportFailureTimeout)
	}
}

// hungFacts are Console facts whose refresh does not return until its
// deadline: a Console that does not answer.
type hungFacts struct {
	*factsStub
	entered chan struct{}
}

func (f *hungFacts) Refresh(ctx context.Context) {
	select {
	case f.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
}

// The Console's facts are asked in the Console round, not the index round: a
// facts refresh held by a silent Console delays no index read.
func TestAHungFactsRefreshDelaysNoIndexRead(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	options.ReconcileInterval, options.CalibrationMaxAge = 200*time.Millisecond, 400*time.Millisecond
	facts := &hungFacts{factsStub: confirmedFacts(), entered: make(chan struct{}, 1)}
	options.Facts = facts
	cache := mustIndex(t, options)
	done := make(chan struct{})
	go func() { defer close(done); cache.consoleRound(context.Background()) }()
	<-facts.entered
	refreshed := make(chan struct{})
	go func() { defer close(refreshed); cache.refreshIndex(context.Background()) }()
	select {
	case <-refreshed:
	case <-testwait.Expired(t):
		t.Fatal("the index round waited on the Console's facts")
	}
	select {
	case <-done:
	case <-testwait.Expired(t):
		t.Fatal("a hung facts refresh outlived its deadline")
	}
}

// A calibration's requests are bounded by the Console's observed tail, not
// only by the calibration interval: with the Console answering in 10 ms, a
// request it never answers ends near four times that, long before a
// 30-second interval would.
func TestACalibrationRequestEndsAtTheConsolesTail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == testLinkdAPIRoot+"/strategy-index/targets" {
			_ = json.NewEncoder(w).Encode([]TargetBinding{testBinding()})
			return
		}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	reader := classReader(t, server.URL, server.Client(), nil)
	for index := 0; index < consoleLatencyColdStart; index++ {
		reader.Latency().Observe(10 * time.Millisecond)
	}
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	options.ReconcileInterval, options.CalibrationMaxAge, options.ReconcileBatch = 30*time.Second, time.Minute, 1
	options.Reconciler = reader
	cache := mustIndex(t, options)
	if err := cache.SetTracked([]StrategyKey{keyA}); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	cache.calibrate(context.Background())
	if took := time.Since(started); took > 5*time.Second {
		t.Fatalf("a calibration took %v against a silent Console, want it ended at the request deadline", took)
	}
}

// The Console round runs at once when the copy starts and whenever a wake
// asks for work, not only on its interval: with an hour between rounds, the
// first calibration comes at start and a requested one right after a move.
func TestTheConsoleRoundRunsAtStartAndOnAWake(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	options.RefreshInterval = time.Hour
	// A subscription that never reports ready: no readiness wake either.
	options.Subscriber = subscriberFunc(func(ctx context.Context, _ func(bool), _ func(StrategyKey), _ func(NoticeRefusal)) error {
		<-ctx.Done()
		return ctx.Err()
	})
	calls := make(chan StrategyKey, 8)
	options.Reconciler = reconcilerFunc(func(_ context.Context, key StrategyKey) (Reconciliation, error) {
		calls <- key
		return Reconciliation{}, nil
	})
	cache := mustIndex(t, options)
	if err := cache.SetTracked([]StrategyKey{keyA}); err != nil {
		t.Fatal(err)
	}
	// Tracking woke the round; that wake is taken here, so the first
	// calibration can only be the one at start.
	select {
	case <-cache.index.consoleWake:
	default:
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = cache.Run(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case <-calls:
	case <-testwait.Expired(t):
		t.Fatal("no calibration at start with an hour between rounds")
	}
	cache.LocationMoved()
	select {
	case <-calls:
	case <-testwait.Expired(t):
		t.Fatal("no calibration after a move asked for one")
	}
}

// Requests that failed before the Console answered -- a reset, a refused
// connection -- are not its latency: a long run of them is counted apart and
// leaves the deadline where the Console's answers put it, so its next real
// answer is not cut. An answer of any status is a sample.
func TestFastFailuresDoNotPullTheConsoleDeadlineDown(t *testing.T) {
	var mode atomic.Int32 // 0 reset, 1 slow answer, 2 quick 502
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load() {
		case 0:
			if hijacker, ok := w.(http.Hijacker); ok {
				if conn, _, err := hijacker.Hijack(); err == nil {
					_ = conn.Close()
					return
				}
			}
		case 2:
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		time.Sleep(300 * time.Millisecond) // fixed-wait: the stub Console's slow answer, which the case sets under the 400 ms deadline it must not be cut by
		_ = json.NewEncoder(w).Encode([]TargetBinding{testBinding()})
	}))
	t.Cleanup(server.Close)
	reader := classReader(t, server.URL, server.Client(), nil)
	for index := 0; index < consoleLatencyColdStart; index++ {
		reader.Latency().Observe(100 * time.Millisecond)
	}
	ctx, cancel := context.WithCancel(testwait.Context(t))
	defer cancel()
	ask := func() error {
		var targets []TargetBinding
		return reader.get(WithRequestCoverage(ctx, time.Minute), "targets", nil, &targets)
	}
	burst := 2 * consoleLatencySamples
	for index := 0; index < burst; index++ {
		if err := ask(); err == nil {
			t.Fatal("a resetting Console answered")
		}
	}
	if samples, unanswered := len(reader.Latency().samples), reader.Latency().Unanswered(); samples != consoleLatencyColdStart || unanswered != uint64(burst) {
		t.Fatalf("after %d resets: %d samples, %d unanswered; want the resets counted apart", burst, samples, unanswered)
	}
	if record := reader.Record(); record.UnansweredRequests != uint64(burst) {
		t.Fatalf("the Console record shows %d unanswered", record.UnansweredRequests)
	}
	if deadline := reader.Latency().Deadline(time.Minute); deadline != 400*time.Millisecond {
		t.Fatalf("after the resets the deadline is %v, want the pre-outage 400 ms", deadline)
	}
	mode.Store(1)
	if err := ask(); err != nil {
		t.Fatalf("the Console's next real answer was cut: %v", err)
	}
	mode.Store(2)
	if err := ask(); err == nil {
		t.Fatal("a 502 read as an answer")
	}
	if samples := len(reader.Latency().samples); samples != consoleLatencyColdStart+2 {
		t.Fatalf("%d samples, want the real answer and the 502 both recorded", samples)
	}
}
