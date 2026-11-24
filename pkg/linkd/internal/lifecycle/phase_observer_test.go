// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/store"
	"linkd/internal/store/memory"
)

type phaseRecorder struct {
	mu      sync.Mutex
	samples []PhaseObservation
}

func (r *phaseRecorder) Finished(_ context.Context, sample PhaseObservation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.samples = append(r.samples, sample)
}

func TestPhaseTimingExcludesNestedTime(t *testing.T) {
	start := time.Now()
	timing := &phaseTiming{started: start, last: start}
	timing.change(phaseApply, start.Add(time.Millisecond))
	timing.change(phaseHook, start.Add(3*time.Millisecond))
	timing.change(phaseApply, start.Add(8*time.Millisecond))
	timing.change(phaseEventCommit, start.Add(10*time.Millisecond))
	timing.change(phaseApply, start.Add(14*time.Millisecond))
	sample := timing.observation(start.Add(15*time.Millisecond), ProcessResult{}, nil)
	want := map[string]time.Duration{"other": time.Millisecond, "apply": 5 * time.Millisecond, "final_hook": 5 * time.Millisecond, "event_commit": 4 * time.Millisecond}
	var sum time.Duration
	for _, phase := range sample.Phases {
		if phase.Duration != want[phase.Phase] {
			t.Errorf("%s = %v, want %v", phase.Phase, phase.Duration, want[phase.Phase])
		}
		sum += phase.Duration
	}
	if sum != sample.Duration || sum != 15*time.Millisecond {
		t.Fatalf("sum=%v total=%v", sum, sample.Duration)
	}
}

func TestPhaseObserverNormalReplayFailureAndCancellation(t *testing.T) {
	repo := memory.New()
	p := newTestProcessor(t, repo, NoopFinalHook{})
	recorder := &phaseRecorder{}
	WithPhaseObserver(recorder)(p)
	event := testEvent("timed-event", "warning")
	result := persistAndProcess(t, repo, p, event)
	stored := mustGetStoredEvent(t, repo, event)
	if _, err := p.ProcessEvent(t.Context(), stored); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ProcessEvent(t.Context(), store.StoredEvent{}); err == nil {
		t.Fatal("expected validation failure")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.ProcessEvent(ctx, stored); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(recorder.samples) != 4 {
		t.Fatalf("samples=%d", len(recorder.samples))
	}
	for i, sample := range recorder.samples {
		assertPhaseSample(t, sample)
		wantSource := event.EventSourceID
		if i == 2 {
			wantSource = ""
		}
		if sample.EventSourceID != wantSource {
			t.Fatalf("attempt %d source=%q want=%q", i, sample.EventSourceID, wantSource)
		}
	}
	if recorder.samples[0].Result.Outcome != result.Outcome || recorder.samples[1].Result.Outcome != result.Outcome {
		t.Fatalf("outcomes: %#v", recorder.samples)
	}
	for _, phase := range recorder.samples[0].Phases {
		if phase.Duration <= 0 {
			t.Errorf("normal attempt did not time %s", phase.Phase)
		}
	}
	for _, sample := range recorder.samples[1:] {
		for _, phase := range sample.Phases {
			if phase.Phase != "other" && phase.Duration != 0 {
				t.Errorf("unexecuted phase %s = %v", phase.Phase, phase.Duration)
			}
		}
	}
	if recorder.samples[2].Err == nil || !errors.Is(recorder.samples[3].Err, context.Canceled) {
		t.Fatal("observer lost failure result")
	}
	if p.phaseTiming != nil {
		t.Fatal("per-attempt timing leaked to shared Processor")
	}
}

func TestPhaseObserverConcurrentEvents(t *testing.T) {
	repo := memory.New()
	p := newTestProcessor(t, repo, NoopFinalHook{})
	r := &phaseRecorder{}
	WithPhaseObserver(r)(p)
	var wg sync.WaitGroup
	for i := range 16 {
		event := testEvent(fmt.Sprintf("timed-%d", i), "warning")
		event.Fingerprint = fmt.Sprintf("fingerprint-%d", i)
		event.EventSourceID = fmt.Sprintf("source-%d", i)
		created, err := repo.CreateEvent(t.Context(), event)
		if err != nil {
			t.Fatal(err)
		}
		wg.Go(func() {
			if _, err := p.ProcessEvent(t.Context(), created.StoredEvent); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if len(r.samples) != 16 {
		t.Fatalf("samples=%d", len(r.samples))
	}
	sources := make(map[string]int)
	for _, sample := range r.samples {
		assertPhaseSample(t, sample)
		sources[sample.EventSourceID]++
	}
	for i := range 16 {
		if sources[fmt.Sprintf("source-%d", i)] != 1 {
			t.Fatalf("concurrent source samples=%v", sources)
		}
	}
}

type phaseFailRepository struct {
	store.Repository
	failure error
}

func (r phaseFailRepository) CompareAndSetEventResult(context.Context, string, string, store.VersionToken, store.EventResult) (store.StoredEvent, error) {
	return store.StoredEvent{}, r.failure
}

func TestPhaseObserverKeepsNestedStorageFailure(t *testing.T) {
	failure := errors.New("event write failed")
	repo := phaseFailRepository{Repository: memory.New(), failure: failure}
	p := newTestProcessor(t, repo, NoopFinalHook{})
	r := &phaseRecorder{}
	WithPhaseObserver(r)(p)
	created, err := repo.CreateEvent(t.Context(), testEvent("phase-write-failure", "warning"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.ProcessEvent(t.Context(), created.StoredEvent); !errors.Is(err, failure) {
		t.Fatalf("returned error=%v", err)
	}
	if len(r.samples) != 1 || !errors.Is(r.samples[0].Err, failure) {
		t.Fatalf("samples=%#v", r.samples)
	}
	assertPhaseSample(t, r.samples[0])
	for _, phase := range r.samples[0].Phases {
		if phase.Phase == "event_commit" && phase.Duration <= 0 {
			t.Fatal("missing failed write duration")
		}
		if (phase.Phase == "apply" || phase.Phase == "final_hook") && phase.Duration <= 0 {
			t.Fatal("missing work before final result failure")
		}
	}
}

func assertPhaseSample(t *testing.T, sample PhaseObservation) {
	t.Helper()
	if len(sample.Phases) != phaseCount {
		t.Fatalf("phases=%d", len(sample.Phases))
	}
	if len(sample.Steps) != len(phaseSteps) {
		t.Fatalf("steps=%d", len(sample.Steps))
	}
	for _, phase := range sample.Phases {
		var wall, batch, queue time.Duration
		for _, step := range sample.Steps {
			if step.Phase != phase.Phase {
				continue
			}
			wall += step.Duration
			batch += step.BatchWait
			queue += step.StorageQueue
		}
		if wall != phase.Duration || batch != phase.BatchWait || queue != phase.StorageQueue {
			t.Fatalf("%s children=(%v,%v,%v) parent=%#v", phase.Phase, wall, batch, queue, phase)
		}
	}

	var sum time.Duration
	for _, phase := range sample.Phases {
		if phase.Duration < 0 {
			t.Fatalf("negative duration: %#v", phase)
		}
		sum += phase.Duration
	}
	if sum != sample.Duration {
		t.Fatalf("sum=%v total=%v", sum, sample.Duration)
	}
}

func TestPhaseTimingAttributesWaitToInnermostStage(t *testing.T) {
	start := time.Now()
	timing := &phaseTiming{started: start, last: start}
	timing.change(phaseEnrich, start)
	timing.observeWait(store.WaitBatch, 2*time.Millisecond)
	timing.observeWait(store.WaitStorageQueue, time.Millisecond)
	timing.change(phaseEventCommit, start.Add(5*time.Millisecond))
	timing.observeWait(store.WaitBatch, 3*time.Millisecond)
	timing.change(phaseEnrich, start.Add(9*time.Millisecond))
	timing.observeWait(store.WaitBatch, time.Millisecond)
	sample := timing.observation(start.Add(12*time.Millisecond), ProcessResult{}, nil)
	var execution, batch, queue time.Duration
	for _, p := range sample.Phases {
		execution += p.Duration - p.BatchWait - p.StorageQueue
		batch += p.BatchWait
		queue += p.StorageQueue
		if p.Phase == "enrich" && (p.Duration != 8*time.Millisecond || p.BatchWait != 3*time.Millisecond || p.StorageQueue != time.Millisecond) {
			t.Fatalf("enrich=%#v", p)
		}
		if p.Phase == "event_commit" && (p.Duration != 4*time.Millisecond || p.BatchWait != 3*time.Millisecond) {
			t.Fatalf("commit=%#v", p)
		}
	}
	if execution != 5*time.Millisecond || batch != 6*time.Millisecond || queue != time.Millisecond || execution+batch+queue != sample.Duration {
		t.Fatalf("execution=%v batch=%v queue=%v sample=%v", execution, batch, queue, sample.Duration)
	}
}

func TestStepTimingExcludesNestedPhasesAndAttributesWaits(t *testing.T) {
	start := time.Now()
	timing := &phaseTiming{started: start, last: start}
	timing.change(phaseApply, start)
	timing.step = stepIndex(phaseApply, "action")
	timing.observeWait(store.WaitBatch, time.Millisecond)
	timing.change(phaseApply, start.Add(3*time.Millisecond))
	timing.step = stepIndex(phaseApply, "alert_cas")
	timing.observeWait(store.WaitStorageQueue, time.Millisecond)
	timing.change(phaseHook, start.Add(5*time.Millisecond))
	timing.change(phaseApply, start.Add(9*time.Millisecond))
	timing.change(phaseApply, start.Add(10*time.Millisecond))
	timing.step = stepIndex(phaseApply, "action")
	sample := timing.observation(start.Add(12*time.Millisecond), ProcessResult{}, nil)
	assertPhaseSample(t, sample)
	for _, step := range sample.Steps {
		if step.Phase == "apply" && step.Step == "action" && (step.Duration != 5*time.Millisecond || step.BatchWait != time.Millisecond) {
			t.Fatal(step)
		}
		if step.Phase == "apply" && step.Step == "alert_cas" && (step.Duration != 3*time.Millisecond || step.StorageQueue != time.Millisecond) {
			t.Fatal(step)
		}
		if step.Phase == "final_hook" && step.Step == "other" && step.Duration != 4*time.Millisecond {
			t.Fatal(step)
		}
	}
}

func TestEnrichProcessorStepNamesAreBoundedAndRepeatedCallsAccumulate(t *testing.T) {
	start := time.Now()
	timing := &phaseTiming{started: start, last: start}
	p := &Processor{phaseTiming: timing}
	timing.change(phaseEnrich, start)
	for _, name := range []string{"strategy", "strategy", "user-supplied-identity"} {
		finish := p.measureEnrichProcessor(name)
		if got := phaseSteps[timing.step].name; got != name && (name != "user-supplied-identity" || got != "custom") {
			t.Fatal(got)
		}
		finish()
	}
	sample := timing.observation(time.Now(), ProcessResult{}, nil)
	assertPhaseSample(t, sample)
	for _, step := range sample.Steps {
		if step.Phase == "enrich" && (step.Step == "strategy" || step.Step == "custom") && step.Duration <= 0 {
			t.Fatal(step)
		}
	}
	if timing.step != 0 {
		t.Fatal("processor span leaked")
	}
}

type phaseTestEnrichProcessor struct{ calls int }

func (*phaseTestEnrichProcessor) Name() string { return "strategy" }

func (*phaseTestEnrichProcessor) Match(context.Context, *enrich.Scope) (bool, error) {
	return true, nil
}

func (p *phaseTestEnrichProcessor) Process(context.Context, *enrich.Scope) (enrich.ProcessorResult, error) {
	p.calls++
	return enrich.ProcessorResult{Status: domain.EnrichStatusSucceeded, Value: domain.JSONObject{}}, nil
}

func TestEventDrilldownUsesContextAndKeepsDurableCheckpoints(t *testing.T) {
	repo := memory.New()
	p := newTestProcessor(t, repo, NoopFinalHook{})
	processor := &phaseTestEnrichProcessor{}
	chain, err := enrich.NewChain([]enrich.Processor{processor}, enrich.Sources{})
	if err != nil {
		t.Fatal(err)
	}
	p.enricher = chain
	recorder := &phaseRecorder{}
	WithPhaseObserver(recorder)(p)
	event := testEvent("step-chain", "warning")
	event.Evaluations = append(event.Evaluations, domain.EventEvaluation{Severity: "critical", Action: domain.EventActionTriggered})
	persistAndProcess(t, repo, p, event)
	if processor.calls != 2 || len(recorder.samples) != 1 {
		t.Fatalf("calls=%d samples=%d", processor.calls, len(recorder.samples))
	}
	sample := recorder.samples[0]
	assertPhaseSample(t, sample)
	for _, expected := range []struct{ phase, step string }{{"enrich", "strategy"}, {"enrich", "persist"}, {"event_commit", "result_save"}, {"apply", "alert_create"}} {
		found := false
		for _, step := range sample.Steps {
			if step.Phase == expected.phase && step.Step == expected.step && step.Duration > 0 {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing measured step: %+v", expected)
		}
	}
}
