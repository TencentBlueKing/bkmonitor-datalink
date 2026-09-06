// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
)

func TestAggregateStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		values []domain.EnrichStatus
		want   domain.EnrichStatus
	}{
		{name: "empty", want: domain.EnrichStatusSucceeded},
		{name: "all succeeded", values: []domain.EnrichStatus{domain.EnrichStatusSucceeded, domain.EnrichStatusSucceeded}, want: domain.EnrichStatusSucceeded},
		{name: "all failed", values: []domain.EnrichStatus{domain.EnrichStatusFailed, domain.EnrichStatusFailed}, want: domain.EnrichStatusFailed},
		{name: "partial", values: []domain.EnrichStatus{domain.EnrichStatusSucceeded, domain.EnrichStatusPartial}, want: domain.EnrichStatusPartial},
		{name: "mixed", values: []domain.EnrichStatus{domain.EnrichStatusSucceeded, domain.EnrichStatusFailed}, want: domain.EnrichStatusPartial},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			results := make([]ProcessorResult, len(test.values))
			for index, status := range test.values {
				results[index].Status = status
			}
			if got := aggregateStatus(results); got != test.want {
				t.Fatalf("aggregateStatus() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestChainOrderIsolationAndFailureContinuation(t *testing.T) {
	t.Parallel()
	alert := testAlert()
	original := alert.Clone()
	first := &testProcessor{name: "first", fn: func(_ context.Context, scope *Scope) (ProcessorResult, error) {
		copy := scope.Alert()
		copy.Labels["bk_strategy_id"] = domain.NewBoolScalar(false)
		copy.Dimensions["host"] = domain.NewStringScalar("changed")
		copy.ExtraData["nested"] = json.RawMessage(`{"changed":true}`)
		return ProcessorResult{Status: domain.EnrichStatusSucceeded, Value: domain.JSONObject{}}, nil
	}}
	broken := &testProcessor{name: "broken", fn: func(context.Context, *Scope) (ProcessorResult, error) {
		return ProcessorResult{}, errors.New("sensitive dependency detail")
	}}
	last := &testProcessor{name: "last", fn: func(_ context.Context, scope *Scope) (ProcessorResult, error) {
		if !reflect.DeepEqual(scope.Alert(), original) {
			t.Fatal("scope alert changed across processors")
		}
		return ProcessorResult{Status: domain.EnrichStatusSucceeded, Value: domain.JSONObject{"ok": json.RawMessage(`true`)}}, nil
	}}
	chain, err := NewChain([]Processor{first, broken, last}, Sources{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: alert})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != domain.EnrichStatusPartial || !reflect.DeepEqual(alert, original) {
		t.Fatalf("result=%#v alert changed=%v", result, !reflect.DeepEqual(alert, original))
	}
	payload, err := DecodePayload(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	wantNames := []string{"first", "broken", "last"}
	for index, entry := range payload.Processors {
		if _, exists := entry[wantNames[index]]; !exists || len(entry) != 1 {
			t.Fatalf("processors[%d]=%#v", index, entry)
		}
	}
	if got := payload.Processors[1]["broken"].Diagnostics[0]; got.Code != DiagnosticCodeDependencyInvalid || got.Dependency != "broken" {
		t.Fatalf("broken diagnostic=%#v", got)
	}
}

func TestChainCancellationStopsFollowingProcessor(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	calls := 0
	first := &testProcessor{name: "first", fn: func(context.Context, *Scope) (ProcessorResult, error) {
		calls++
		cancel()
		return ProcessorResult{Status: domain.EnrichStatusSucceeded, Value: domain.JSONObject{}}, nil
	}}
	last := &testProcessor{name: "last", fn: func(context.Context, *Scope) (ProcessorResult, error) {
		calls++
		return ProcessorResult{Status: domain.EnrichStatusSucceeded, Value: domain.JSONObject{}}, nil
	}}
	chain, _ := NewChain([]Processor{first, last}, Sources{})
	if _, err := chain.Enrich(cancelled, lifecycle.EnrichInput{Alert: testAlert()}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Enrich() error=%v", err)
	}
	if calls != 1 {
		t.Fatalf("processor calls=%d, want 1", calls)
	}
}

func TestValidateRequiredIDs(t *testing.T) {
	t.Parallel()
	alert := testAlert()
	ids, diagnostics := ValidateRequiredIDs(alert)
	if len(diagnostics) != 0 || ids != (RequiredIDs{StrategyID: 123, HistoryID: 70001, BizID: 2}) {
		t.Fatalf("valid IDs=%#v diagnostics=%#v", ids, diagnostics)
	}
	delete(alert.Labels, labelStrategyHistoryID)
	alert.Labels[labelBizID] = domain.NewStringScalar("2")
	_, diagnostics = ValidateRequiredIDs(alert)
	if len(diagnostics) != 2 || diagnostics[0].Code != DiagnosticCodeMissingField || diagnostics[1].Code != DiagnosticCodeInvalidField {
		t.Fatalf("invalid diagnostics=%#v", diagnostics)
	}
}

func TestNoopPayload(t *testing.T) {
	result, err := (NoopEnricher{}).Enrich(context.Background(), lifecycle.EnrichInput{Alert: testAlert()})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := DecodePayload(result.Data)
	if err != nil || payload.Status != domain.EnrichStatusSucceeded || len(payload.Processors) != 0 {
		t.Fatalf("payload=%#v error=%v", payload, err)
	}
}

type testProcessor struct {
	name string
	fn   func(context.Context, *Scope) (ProcessorResult, error)
}

func (p *testProcessor) Name() string { return p.name }
func (p *testProcessor) Match(context.Context, *Scope) (bool, error) {
	return true, nil
}
func (p *testProcessor) Process(ctx context.Context, scope *Scope) (ProcessorResult, error) {
	return p.fn(ctx, scope)
}

func testAlert() domain.Alert {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	strategyID, _ := domain.NewNumberScalar(123)
	historyID, _ := domain.NewNumberScalar(70001)
	bizID, _ := domain.NewNumberScalar(2)
	return domain.Alert{
		AlertID: "alert-1", BKTenantID: "tenant-1", EventSourceID: "built_in_bk", Fingerprint: "fp",
		Title: "CPU high", Content: "usage is high", Severity: "warning", Dimensions: domain.DimensionMap{"host": domain.NewStringScalar("host-1")},
		Labels:    domain.DimensionMap{labelStrategyID: strategyID, labelStrategyHistoryID: historyID, labelBizID: bizID},
		ExtraData: domain.JSONObject{"nested": json.RawMessage(`{"value":1}`)}, Status: domain.AlertStatusActive,
		LatestEventID: "event-1", TriggerEventID: "event-1", SourceEventID: "source-event-1",
		LastOccurredAt: now, UpdateAt: now, BeginAt: now, CreateAt: now,
		EnrichStatus: domain.EnrichStatusPending, Enrich: domain.JSONObject{},
	}
}
