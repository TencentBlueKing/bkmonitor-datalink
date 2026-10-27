// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich/models"
)

func TestChainActiveReusePreservesOrderContextAndCurrentFields(t *testing.T) {
	t.Parallel()
	calls := 0
	observer := &recordingProcessorObserver{}
	resource := &testProcessor{name: "monitor-resource", fn: func(_ context.Context, scope *Scope) (ProcessorResult, error) {
		calls++
		values := models.ResourceValues{ModelID: "cw-Host", ModelInstID: "host-1", BKBizName: "original business", BKHostID: json.Number("9007199254740993")}
		scope.Context().Resource.Set(values)
		value, err := ValuesObject(values)
		return ProcessorResult{Status: domain.EnrichStatusSucceeded, Value: value}, err
	}}
	fields := &testProcessor{name: "fields", fn: func(_ context.Context, scope *Scope) (ProcessorResult, error) {
		value := scope.Context().Resource.Values
		if value.ModelID != "cw-Host" || value.BKBizName != "original business" || fmt.Sprint(value.BKHostID) != "9007199254740993" {
			return ProcessorResult{}, fmt.Errorf("resource context was not restored: %+v", value)
		}
		state, err := scope.EvaluationState()
		if err != nil {
			return ProcessorResult{}, err
		}
		labels := state.Document()["labels"].(map[string]any)
		if labels["model_id"] != "cw-Host" {
			return ProcessorResult{}, errors.New("previous processor patch was not applied")
		}
		patch, err := domain.NewEnrichPatch("$.title", scope.Facts().Content+":"+value.BKBizName)
		return ProcessorResult{Status: domain.EnrichStatusSucceeded, Patches: []domain.EnrichPatch{patch}}, err
	}}
	chain, err := NewChain([]Processor{resource, fields}, Sources{}, WithReuseOnActive("monitor-resource"), WithObserver(observer))
	if err != nil {
		t.Fatal(err)
	}
	opening := testAlert()
	first, err := chain.Enrich(t.Context(), Input{Event: opening})
	if err != nil {
		t.Fatal(err)
	}
	active := activeReuseSnapshot(opening, first)
	before := active.Clone()
	incoming := opening.Clone()
	incoming.EventID = "next-event"
	incoming.Content = "current detection value"
	second, err := chain.Enrich(t.Context(), Input{Event: incoming, ActiveAlert: &active})
	if err != nil || second.Status != domain.EnrichStatusSucceeded {
		t.Fatalf("result=%+v err=%v", second, err)
	}
	payload, err := DecodePayload(second.Data.Evaluations[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || payload.Processors[0]["monitor-resource"].ReusedFromAlertID != active.AlertID || payload.Processors[1]["fields"].ReusedFromAlertID != "" {
		t.Fatalf("calls=%d payload=%+v", calls, payload)
	}
	if title := string(payload.Processors[1]["fields"].Patches[0].Value); title != `"current detection value:original business"` {
		t.Fatal("current event was replaced by the opening event:", title)
	}
	if !reflect.DeepEqual(active, before) || incoming.Title != opening.Title {
		t.Fatal("reuse changed the active snapshot or source event")
	}
	if observer.values[2].Outcome != ProcessorOutcomeReused || observer.values[3].Outcome != ProcessorOutcomeCompleted {
		t.Fatalf("observations=%+v", observer.values)
	}
}

func TestChainActiveReuseFallsBackToExecution(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		change   func(*Input)
		disabled bool
		status   domain.EnrichStatus
	}{
		{name: "default disabled", disabled: true},
		{name: "no active alert", change: func(input *Input) { input.ActiveAlert = nil }},
		{name: "recovered alert", change: func(input *Input) { input.ActiveAlert.Status = domain.AlertStatusRecovered }},
		{name: "closed alert", change: func(input *Input) { input.ActiveAlert.Status = domain.AlertStatusClosed }},
		{name: "other tenant", change: func(input *Input) { input.ActiveAlert.BKTenantID = "other" }},
		{name: "other source", change: func(input *Input) { input.ActiveAlert.EventSourceID = "other" }},
		{name: "other fingerprint", change: func(input *Input) { input.ActiveAlert.Fingerprint = "other" }},
		{name: "unknown snapshot severity", change: func(input *Input) { input.ActiveAlert.EnrichSeverity = "" }},
		{name: "different snapshot severity", change: func(input *Input) { input.ActiveAlert.EnrichSeverity = "critical" }},
		{name: "preview runs processors", change: func(input *Input) { input.Preview = true }},
		{name: "processor absent from snapshot", change: func(input *Input) { input.ActiveAlert.Enrich["processors"] = json.RawMessage(`[]`) }},
		{name: "partial is retried", status: domain.EnrichStatusPartial},
		{name: "failed is retried", status: domain.EnrichStatusFailed},
		{name: "skipped is reevaluated", status: domain.EnrichStatusSkipped},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			processor := &testProcessor{name: "fields", fn: func(context.Context, *Scope) (ProcessorResult, error) {
				calls++
				status := domain.EnrichStatusSucceeded
				if calls == 1 && test.status != "" {
					status = test.status
				}
				return ProcessorResult{Status: status, Patches: []domain.EnrichPatch{}}, nil
			}}
			var options []ChainOption
			if !test.disabled {
				options = append(options, WithReuseOnActive("fields"))
			}
			chain, err := NewChain([]Processor{processor}, Sources{}, options...)
			if err != nil {
				t.Fatal(err)
			}
			event := testAlert()
			first, err := chain.Enrich(t.Context(), Input{Event: event})
			if err != nil {
				t.Fatal(err)
			}
			active := activeReuseSnapshot(event, first)
			input := Input{Event: event, ActiveAlert: &active}
			if test.change != nil {
				test.change(&input)
			}
			result, err := chain.Enrich(t.Context(), input)
			if err != nil || result.Status != domain.EnrichStatusSucceeded || calls != 2 {
				t.Fatalf("calls=%d result=%+v err=%v", calls, result, err)
			}
		})
	}
}

func TestChainActiveReuseMatchesSnapshotSeverityAfterInPlaceUpgrade(t *testing.T) {
	t.Parallel()
	calls := 0
	processor := &testProcessor{name: "fields", fn: func(_ context.Context, scope *Scope) (ProcessorResult, error) {
		calls++
		patch, err := domain.NewEnrichPatch("$.title", scope.Evaluation().Severity+":"+scope.Facts().EventID)
		return ProcessorResult{Status: domain.EnrichStatusSucceeded, Patches: []domain.EnrichPatch{patch}}, err
	}}
	chain, err := NewChain([]Processor{processor}, Sources{}, WithReuseOnActive("fields"))
	if err != nil {
		t.Fatal(err)
	}
	event := testAlert()
	first, err := chain.Enrich(t.Context(), Input{Event: event})
	if err != nil {
		t.Fatal(err)
	}
	active := activeReuseSnapshot(event, first)
	active.Severity = "critical"
	event.EventID = "after-upgrade"
	event.Evaluations = append(event.Evaluations, domain.EventEvaluation{Severity: "critical", Action: domain.EventActionTriggered})
	result, err := chain.Enrich(t.Context(), Input{Event: event, ActiveAlert: &active})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range result.Data.Evaluations {
		payload, err := DecodePayload(row.Data)
		if err != nil {
			t.Fatal(err)
		}
		step := payload.Processors[0]["fields"]
		if (step.ReusedFromAlertID != "") != (row.Severity == "warning") {
			t.Fatalf("snapshot was used for wrong severity: %+v", row)
		}
		if row.Severity == "critical" && string(step.Patches[0].Value) != `"critical:after-upgrade"` {
			t.Fatal("new severity did not execute independently")
		}
	}
	if calls != 2 {
		t.Fatalf("processor calls=%d, want 2", calls)
	}
}

func TestChainActiveReuseConcurrentAndCanceled(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	processor := &testProcessor{name: "fields", fn: func(context.Context, *Scope) (ProcessorResult, error) {
		calls.Add(1)
		patch, err := domain.NewEnrichPatch("$.title", "saved title")
		return ProcessorResult{Status: domain.EnrichStatusSucceeded, Patches: []domain.EnrichPatch{patch}}, err
	}}
	chain, err := NewChain([]Processor{processor}, Sources{}, WithReuseOnActive("fields"))
	if err != nil {
		t.Fatal(err)
	}
	event := testAlert()
	first, err := chain.Enrich(t.Context(), Input{Event: event})
	if err != nil {
		t.Fatal(err)
	}
	active := activeReuseSnapshot(event, first)
	var workers sync.WaitGroup
	results := make(chan error, 16)
	for range 16 {
		workers.Go(func() {
			result, err := chain.Enrich(t.Context(), Input{Event: event, ActiveAlert: &active})
			if err == nil && result.Status != domain.EnrichStatusSucceeded {
				err = fmt.Errorf("unexpected status %s", result.Status)
			}
			results <- err
		})
	}
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := chain.Enrich(ctx, Input{Event: event, ActiveAlert: &active}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled reuse: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("processor calls=%d, want 1", calls.Load())
	}
}

func activeReuseSnapshot(event domain.Event, result Result) domain.Alert {
	return domain.Alert{AlertID: "active-alert", BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID,
		Fingerprint: event.Fingerprint, Status: domain.AlertStatusActive, Severity: event.Evaluations[0].Severity,
		EnrichSeverity: event.Evaluations[0].Severity, EnrichStatus: result.Status, Enrich: result.Data.Evaluations[0].Data.Clone()}
}
