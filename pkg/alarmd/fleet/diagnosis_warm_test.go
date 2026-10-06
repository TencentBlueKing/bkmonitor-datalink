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
	"strings"
	"testing"
	"time"
)

// The warm-up runs on the tick the catalog arrives here, once, and again
// only after it has left and come back: a hand-over, not every tick.
func TestTheDiagnosisWarmUpRunsOncePerHandOver(t *testing.T) {
	holding := false
	lookup := func(string) StrategyLookupFacts { return StrategyLookupFacts{Available: holding} }
	universe := func(context.Context) ([]string, error) { return nil, nil }
	warmer := NewDiagnosisWarmer(nil, lookup, universe, nil, func() time.Time { return now }, 0)
	steps := []struct {
		holding bool
		warm    bool
	}{{false, false}, {true, true}, {true, false}, {true, false}, {false, false}, {true, true}}
	for index, step := range steps {
		holding = step.holding
		if got := warmer.Tick() != nil; got != step.warm {
			t.Fatalf("tick %d holding=%v: warm-up returned %v, want %v", index, step.holding, got, step.warm)
		}
	}
	var unwired *DiagnosisWarmer
	if unwired.Tick() != nil || unwired.Last() != nil {
		t.Fatal("a process without a warmer warmed")
	}
}

// countingRegistry counts the replica-list reads the fleet service makes,
// the read the service keeps for a window and a cold process has not made.
type countingRegistry struct {
	replicas []string
	reads    *int
}

func (registry countingRegistry) ReadyReplicas(context.Context, time.Time) ([]string, error) {
	*registry.reads++
	return registry.replicas, nil
}

// The warm-up goes through every reader a first page goes through -- the
// universe, the fleet's replica list and snapshots, and the page's progress
// -- so a first page after it finds the service's window already read. Its
// timing is what a cold first page would have paid, and every page carries
// it.
func TestTheDiagnosisWarmUpReadsWhatAFirstPageReads(t *testing.T) {
	registryReads, universeReads := 0, 0
	var progressAsked []string
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 3, Known: true, IDs: []string{"qg-4101-a", "qg-4101-b", "qg-other"}}},
		countingRegistry{replicas: replicas(), reads: &registryReads}, stubSnapshots{snapshots: healthySnapshots()})
	facts := diagnosisFacts()
	lookup := func(id string) StrategyLookupFacts {
		if f, ok := facts[id]; ok {
			return f
		}
		return StrategyLookupFacts{Available: true}
	}
	universe := func(context.Context) ([]string, error) {
		universeReads++
		return []string{"4101", "4102"}, nil
	}
	progress := func(_ context.Context, groups []string) (map[string]ProgressFacts, map[string]bool, error) {
		progressAsked = append(progressAsked, groups...)
		return map[string]ProgressFacts{}, map[string]bool{}, nil
	}
	warmer := NewDiagnosisWarmer(service, lookup, universe, progress, func() time.Time { return now }, 0)
	warm := warmer.Tick()
	if warm == nil {
		t.Fatal("no warm-up on taking the catalog")
	}
	warm(context.Background())
	last := warmer.Last()
	if last == nil || last.Error != "" || !last.At.Equal(now) {
		t.Fatalf("last warm-up = %+v", last)
	}
	if last.Timing.UniverseMillis == nil || last.Timing.ViewMillis == nil || last.Timing.ProgressMillis == nil {
		t.Fatalf("warm-up timing = %+v, want universe, view and progress each measured", last.Timing)
	}
	if universeReads != 1 || registryReads != 1 || strings.Join(progressAsked, ",") != "qg-4101-a,qg-4101-b" {
		t.Fatalf("universe reads %d, registry reads %d, progress asked %v; want one each and the first page's groups",
			universeReads, registryReads, progressAsked)
	}

	handler := WithDiagnosis(http.NotFoundHandler(), service, lookup, nil, universe, progress, "pod-a",
		func() time.Time { return now }, 0, warmer)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/diagnose", nil))
	var body DiagnosisResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if registryReads != 1 {
		t.Errorf("registry reads = %d after the first page, want the warm-up's read reused", registryReads)
	}
	if universeReads != 2 {
		t.Errorf("universe reads = %d, want the new diagnosis to read its own", universeReads)
	}
	if body.Warmed == nil || !body.Warmed.At.Equal(now) || body.Warmed.Timing.ViewMillis == nil {
		t.Errorf("page's warmed = %+v, want the warm-up and its timing", body.Warmed)
	}
}

// A warm-up whose universe cannot be read says so and reads nothing after
// it, the way a first page stops there.
func TestAWarmUpThatCannotReadTheUniverseSaysSoAndStops(t *testing.T) {
	asked := false
	warmer := NewDiagnosisWarmer(nil, func(string) StrategyLookupFacts { return StrategyLookupFacts{Available: true} },
		func(context.Context) ([]string, error) { return nil, errors.New("SOURCE_UNREADABLE") },
		func(context.Context, []string) (map[string]ProgressFacts, map[string]bool, error) {
			asked = true
			return nil, nil, nil
		}, func() time.Time { return now }, 0)
	warm := warmer.Warm(context.Background())
	if warm.Error != "SOURCE_UNREADABLE" || warm.Timing.UniverseMillis == nil || warm.Timing.ViewMillis != nil || warm.Timing.ProgressMillis != nil || asked {
		t.Fatalf("warm-up = %+v asked progress %v, want the failure named and nothing read after it", warm, asked)
	}
}

// Every page says where its time went. The first page read the universe and
// the view and says how long each took; a later page answered from the
// diagnosis's cache does not carry those at all, rather than a zero that
// would read as free. Progress is there only where it is wired.
func TestADiagnosisPageSaysWhereItsTimeWent(t *testing.T) {
	rig := newDiagnosisRig(t, diagnosisFacts(), func(context.Context, []string) (map[string]ProgressFacts, map[string]bool, error) {
		return map[string]ProgressFacts{}, map[string]bool{}, nil
	})
	rig.universe = []string{"4101", "4102", "4103"}
	first, firstRaw := rig.rawPage(t, "")
	if first.Timing.UniverseMillis == nil || first.Timing.ViewMillis == nil || first.Timing.ProgressMillis == nil {
		t.Fatalf("first page timing = %+v, want universe, view and progress", first.Timing)
	}
	if first.Warmed != nil || strings.Contains(firstRaw, `"warmed"`) {
		t.Errorf("a process that has not warmed said it had: %s", firstRaw)
	}
	_, laterRaw := rig.rawPage(t, first.NextCursor)
	timing := timingKeys(t, laterRaw)
	if _, ok := timing["universe"]; ok {
		t.Errorf("a cached page carried a universe time: %v", timing)
	}
	if _, ok := timing["view"]; ok {
		t.Errorf("a cached page carried a view time: %v", timing)
	}
	if _, ok := timing["rows"]; !ok {
		t.Errorf("a page without its rows time: %v", timing)
	}

	unwired := newDiagnosisRig(t, diagnosisFacts(), nil)
	unwired.universe = []string{"4101"}
	_, raw := unwired.rawPage(t, "")
	if _, ok := timingKeys(t, raw)["progress"]; ok {
		t.Errorf("a page without progress wired carried a progress time: %s", raw)
	}
}

// rawPage is one page of two rows, decoded and as sent.
func (rig *diagnosisRig) rawPage(t *testing.T, cursor string) (DiagnosisResponse, string) {
	t.Helper()
	target := "/api/diagnose?limit=2"
	if cursor != "" {
		target += "&cursor=" + cursor
	}
	w := httptest.NewRecorder()
	rig.handler.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body DiagnosisResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body, w.Body.String()
}

func timingKeys(t *testing.T, raw string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatal(err)
	}
	timing, ok := decoded["timing_ms"].(map[string]any)
	if !ok {
		t.Fatalf("no timing_ms in %s", raw)
	}
	return timing
}
