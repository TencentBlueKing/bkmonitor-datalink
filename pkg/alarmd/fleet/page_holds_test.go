// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// heldLine is a memory line that says what is held now, how many holds it
// gave, and what it was asked to admit, with what was held at each ask.
type heldLine struct {
	mu          sync.Mutex
	held        uint64
	holds       int
	admitted    []uint64
	heldAtAdmit []uint64
	refuseKeep  bool
}

func (line *heldLine) hold(bytes uint64) (func(), bool) {
	line.mu.Lock()
	line.held += bytes
	line.holds++
	line.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			line.mu.Lock()
			line.held -= bytes
			line.mu.Unlock()
		})
	}, true
}

func (line *heldLine) admit(bytes uint64) bool {
	line.mu.Lock()
	defer line.mu.Unlock()
	line.admitted = append(line.admitted, bytes)
	line.heldAtAdmit = append(line.heldAtAdmit, line.held)
	return !line.refuseKeep
}

func (line *heldLine) reading() (uint64, int) {
	line.mu.Lock()
	defer line.mu.Unlock()
	return line.held, line.holds
}

// heldAtWrite is a response that records what the line held when the
// answer was written.
type heldAtWrite struct {
	*httptest.ResponseRecorder
	line *heldLine
	held uint64
}

func (response *heldAtWrite) Write(body []byte) (int, error) {
	response.held, _ = response.line.reading()
	return response.ResponseRecorder.Write(body)
}

// pageRig is a store of two replicas' snapshots wired to a heldLine, the
// stored length of the snapshots, and the service over them.
func pageRig(t *testing.T) (*RedisStore, *fakeRedis, *heldLine, uint64, *Service) {
	t.Helper()
	client := newFakeRedis()
	store := mustStore(t, client, time.Minute, 0)
	stored := uint64(0)
	for _, snapshot := range snapshotsWithAnomalies(2) {
		snapshot.TakenAt = now
		encoded, _ := json.Marshal(snapshot)
		client.values[store.snapshotKey(snapshot.Replica)] = string(encoded)
		stored += uint64(len(encoded))
	}
	line := &heldLine{}
	store.AdmitLoads(line.admit)
	store.HoldLoads(line.hold)
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}}, stubRegistry{replicas: replicas()}, store)
	return store, client, line, stored, service
}

// Every page that reads the fleet's snapshots holds what they and the view
// built of them come to -- their stored length times pageReadCharge -- while
// it writes its answer, and holds nothing once it has: an idle process holds
// nothing on the line. None of it is admitted.
func TestAPageHoldsWhatItReadsUntilItsAnswerIsWritten(t *testing.T) {
	lookup := func(string) StrategyLookupFacts { return StrategyLookupFacts{Available: true} }
	universe := func(context.Context) ([]string, error) { return []string{"854"}, nil }
	for _, target := range []string{"/api/objects", "/api/objects/qg-001", "/api/strategies", "/api/strategies/854",
		"/api/diagnose?summary=1"} {
		_, _, line, stored, service := pageRig(t)
		pages, err := NewHandler(service, nil, func() time.Time { return now }, time.Minute, nil, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		handler := WithDiagnosis(WithStrategyStanding(pages, service, lookup, nil, nil, nil, "pod-a", func() time.Time { return now },
			time.Minute), service, lookup, nil, universe, nil, "pod-a", func() time.Time { return now }, time.Minute, nil)
		response := &heldAtWrite{ResponseRecorder: httptest.NewRecorder(), line: line}
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		held, holds := line.reading()
		want := stored * pageReadChargeNum / pageReadChargeDen
		if response.Code != http.StatusOK || holds != 1 || response.held != want || held != 0 || len(line.admitted) != 0 {
			t.Errorf("%s: answered %d, %d holds, %d held at the write (want %d), %d held after, admitted %v; "+
				"want one hold of the charge while writing and nothing after", target, response.Code, holds, response.held, want, held,
				line.admitted)
		}
	}
}

// A page whose read took its hold and then failed -- a snapshot that does
// not decode -- still releases it.
func TestAPageWhoseReadFailedReleasesItsHold(t *testing.T) {
	store, client, line, _, service := pageRig(t)
	client.values[store.snapshotKey("pod-b")] = "{not json"
	handler, err := NewHandler(service, nil, func() time.Time { return now }, time.Minute, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	code, body := get(t, handler, "/api/objects")
	if held, holds := line.reading(); code != http.StatusOK || holds != 1 || held != 0 {
		t.Fatalf("answered %d (%v), %d holds, %d held after; want the failed read's hold released", code, body["gaps"], holds, held)
	}
}

// A shared read holds once for everyone who takes its answer, and lets go
// when the last of them has: not when the caller that started it is done,
// and not later than the read itself for callers that all stopped waiting.
func TestASharedReadHoldsUntilItsLastUserLetsGo(t *testing.T) {
	line := &heldLine{}
	var reads sharedReads[loaded]
	started, finish := make(chan struct{}), make(chan struct{})
	read := func(ctx context.Context) loaded {
		release, _ := line.hold(100)
		pageHoldsOf(ctx).add(release, 100)
		close(started)
		<-finish
		return loaded{snapshots: []Snapshot{{Replica: "pod-a"}}}
	}
	gone := func(err error) loaded { return loaded{err: err} }
	first, releaseFirst := withPageHolds(context.Background())
	second, releaseSecond := withPageHolds(context.Background())
	answers := make(chan loaded, 2)
	go func() { answers <- reads.do(first, "k", read, gone) }()
	<-started
	go func() { answers <- reads.do(second, "k", read, gone) }()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		reads.mu.Lock()
		joined := reads.flights["k"] != nil && reads.flights["k"].waiters == 2
		reads.mu.Unlock()
		if joined {
			break
		}
	}
	close(finish)
	<-answers
	<-answers
	releaseFirst()
	if held, holds := line.reading(); held != 100 || holds != 1 {
		t.Fatalf("%d held from %d holds with one page still using the answer, want the one hold kept", held, holds)
	}
	if stored := pageHoldsOf(second).storedBytes(); stored != 100 {
		t.Fatalf("the second page carries %d stored bytes, want the read's", stored)
	}
	releaseSecond()
	if held, _ := line.reading(); held != 0 {
		t.Fatalf("%d held after both pages let go, want none", held)
	}

	// Everyone stops waiting: the hold goes when the stopped read returns.
	started, finish = make(chan struct{}), make(chan struct{})
	leaving, leave := context.WithCancel(context.Background())
	left := make(chan loaded, 1)
	go func() { left <- reads.do(leaving, "k", read, gone) }()
	<-started
	leave()
	if answer := <-left; !errors.Is(answer.err, context.Canceled) {
		t.Fatalf("the caller that left got %v", answer.err)
	}
	close(finish)
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if held, _ := line.reading(); held == 0 {
			return
		}
	}
	t.Fatal("the stopped read's hold was never released")
}

// A diagnosis kept in the cache releases what its read held and then asks
// the line to admit the view it keeps -- the snapshots' stored length times
// keptViewCharge; refused, the diagnosis is answered and not kept. A view
// not read whole is neither admitted nor kept.
func TestAKeptDiagnosisIsAdmittedAfterItsHoldIsReleased(t *testing.T) {
	for name, refuse := range map[string]bool{"admitted": false, "refused": true} {
		_, _, line, stored, service := pageRig(t)
		line.refuseKeep = refuse
		cache := &diagnosisCache{entries: map[string]*diagnosisEntry{}, admitKept: service.admitKeptView}
		entry, _ := cache.get(context.Background(), "", now, func(ctx context.Context) *diagnosisEntry {
			return readDiagnosisEntry(ctx, service, func(context.Context) ([]string, error) { return []string{"854"}, nil }, now, time.Minute)
		})
		held, holds := line.reading()
		_, kept := cache.entries[entry.id]
		want := stored * keptViewChargeNum / keptViewChargeDen
		if holds != 1 || held != 0 || len(line.admitted) != 1 || line.admitted[0] != want || line.heldAtAdmit[0] != 0 || kept == refuse {
			t.Errorf("%s: %d holds, %d held, admitted %v with %v held at the ask (want %d with none), kept %v", name, holds, held,
				line.admitted, line.heldAtAdmit, want, kept)
		}
	}
	store, client, line, _, service := pageRig(t)
	client.values[store.snapshotKey("pod-b")] = "{not json"
	cache := &diagnosisCache{entries: map[string]*diagnosisEntry{}, admitKept: service.admitKeptView}
	entry, _ := cache.get(context.Background(), "", now, func(ctx context.Context) *diagnosisEntry {
		return readDiagnosisEntry(ctx, service, func(context.Context) ([]string, error) { return []string{"854"}, nil }, now, time.Minute)
	})
	if _, kept := cache.entries[entry.id]; kept || len(line.admitted) != 0 {
		t.Fatalf("an unread view was kept %v with admits %v, want neither", kept, line.admitted)
	}
	if held, _ := line.reading(); held != 0 {
		t.Fatalf("%d held after an unread diagnosis, want none", held)
	}
}

// A load under no page's collector is admitted as before, whatever the
// store can hold.
func TestALoadUnderNoPageIsAdmitted(t *testing.T) {
	store, _, line, stored, _ := pageRig(t)
	if _, err := store.Load(context.Background(), replicas()); err != nil {
		t.Fatal(err)
	}
	if _, holds := line.reading(); holds != 0 || len(line.admitted) != 1 || line.admitted[0] != stored*snapshotDecodedCharge {
		t.Fatalf("%d holds, admitted %v; want the load admitted at %d", holds, line.admitted, stored*snapshotDecodedCharge)
	}
}

// A hold added after its work is done is released at once: nothing would
// release it later.
func TestAHoldAddedAfterTheWorkIsDoneIsReleasedAtOnce(t *testing.T) {
	line := &heldLine{}
	holds := &pageHolds{}
	holds.release()
	release, _ := line.hold(10)
	holds.add(release, 10)
	if held, _ := line.reading(); held != 0 {
		t.Fatalf("%d held after a hold was added to finished work, want none", held)
	}
}

// A diagnosis whose read panics after taking its hold still releases it,
// answers the requests waiting on it, and is not kept.
func TestADiagnosisReadThatPanicsReleasesItsHold(t *testing.T) {
	line := &heldLine{}
	cache := &diagnosisCache{entries: map[string]*diagnosisEntry{}}
	func() {
		defer func() { _ = recover() }()
		cache.get(context.Background(), "d-1", now, func(ctx context.Context) *diagnosisEntry {
			release, _ := line.hold(1000)
			pageHoldsOf(ctx).add(release, 1000)
			panic("the read failed")
		})
	}()
	if held, holds := line.reading(); holds != 1 || held != 0 {
		t.Fatalf("%d held from %d holds after the read panicked, want it released", held, holds)
	}
	if len(cache.entries) != 0 {
		t.Fatalf("entries %v after a read that panicked, want none kept", cache.entries)
	}
}

// A request waiting on a diagnosis whose read panics is not left waiting:
// told the read failed, it reads for itself, as a request after a failed
// read does.
func TestARequestWaitingOnAReadThatPanicsIsAnswered(t *testing.T) {
	cache := &diagnosisCache{entries: map[string]*diagnosisEntry{}}
	reading, fail := make(chan struct{}), make(chan struct{})
	go func() {
		defer func() { _ = recover() }()
		cache.get(context.Background(), "d-1", now, func(context.Context) *diagnosisEntry {
			close(reading)
			<-fail
			panic("the read failed")
		})
	}()
	<-reading
	waited := make(chan *diagnosisEntry, 1)
	go func() {
		entry, _ := cache.get(context.Background(), "d-1", now, func(context.Context) *diagnosisEntry {
			return &diagnosisEntry{readError: "a second read"}
		})
		waited <- entry
	}()
	time.Sleep(100 * time.Millisecond)
	close(fail)
	select {
	case entry := <-waited:
		if entry.readError != "a second read" {
			t.Fatalf("the waiting request got %q, want its own read after the failed one", entry.readError)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the request waiting on a read that panicked was never answered")
	}
}

// The diagnosis warm-up holds what its read takes until it is done, as a
// page does: it keeps only its timing.
func TestTheDiagnosisWarmUpReleasesWhatItHeld(t *testing.T) {
	_, _, line, _, service := pageRig(t)
	lookup := func(string) StrategyLookupFacts { return StrategyLookupFacts{Available: true} }
	warmer := NewDiagnosisWarmer(service, lookup, func(context.Context) ([]string, error) { return []string{"854"}, nil }, nil,
		func() time.Time { return now }, time.Minute)
	if warm := warmer.Warm(context.Background()); warm.Error != "" {
		t.Fatalf("warm-up failed: %s", warm.Error)
	}
	if held, holds := line.reading(); holds != 1 || held != 0 {
		t.Fatalf("%d held from %d holds after the warm-up, want one hold released", held, holds)
	}
}

// Each charge covers what it was measured at: a page read's peak at 4.22
// times the stored length, a kept view at 1.92.
func TestTheChargesCoverTheirMeasurements(t *testing.T) {
	if pageReadChargeNum*100 < 422*pageReadChargeDen {
		t.Errorf("a page read is held at %d/%d, below its measured 4.22", pageReadChargeNum, pageReadChargeDen)
	}
	if keptViewChargeNum*100 < 192*keptViewChargeDen {
		t.Errorf("a kept view is admitted at %d/%d, below its measured 1.92", keptViewChargeNum, keptViewChargeDen)
	}
}
