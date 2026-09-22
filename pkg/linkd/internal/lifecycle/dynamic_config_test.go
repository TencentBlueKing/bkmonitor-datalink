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
	"testing"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/memory"
)

type liveSeverityTable map[string]int

func (t liveSeverityTable) Priority(name string) (int, bool) { v, ok := t[name]; return v, ok }

type changingSeverity struct {
	values liveSeverityTable
	digest string
}

func (s *changingSeverity) Priority(name string) (int, bool) { return s.values.Priority(name) }

func (s *changingSeverity) FreezeSeverity() (SeverityTable, string) { return s.values, s.digest }

func TestUnknownSeverityClosesActiveThenRejectsWholeEvent(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	hook := &recordingHook{}
	p := newTestProcessor(t, repo, hook)
	opening := testEvent("opening", "critical")
	created := persistAndProcess(t, repo, p, opening)
	other := testEvent("other-tenant", "critical")
	other.BKTenantID = "tenant-2"
	otherCreated := persistAndProcess(t, repo, p, other)
	p.severity = &changingSeverity{values: liveSeverityTable{"warning": 1}, digest: "new-config"}
	event := testEvent("changed", "critical")
	event.Evaluations = append(event.Evaluations, domain.EventEvaluation{Severity: "warning", Action: domain.EventActionTriggered})
	result := persistAndProcess(t, repo, p, event)
	if result.EventState != domain.EventProcessStateRejected || result.ReasonCode != "unknown_severity" || len(result.AlertIDs) != 0 {
		t.Fatalf("result=%+v", result)
	}
	closed, err := repo.GetAlert(ctx, opening.BKTenantID, created.AlertIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if closed.Alert.Status != domain.AlertStatusClosed || closed.Alert.EndType != domain.AlertEndTypeSystem || closed.Alert.EndReason != "unknown_severity" {
		t.Fatalf("closed=%+v", closed.Alert)
	}
	untouched, err := repo.GetAlert(ctx, other.BKTenantID, otherCreated.AlertIDs[0])
	if err != nil || untouched.Alert.Status != domain.AlertStatusActive {
		t.Fatal("other tenant modified")
	}
	if _, err = repo.FindActiveAlert(ctx, activeKeyForTest(event)); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("mixed event created an alert")
	}
	stored := mustGetStoredEvent(t, repo, event)
	if stored.Processing.ProcessedAt == nil || len(stored.Processing.Evaluations) != 2 {
		t.Fatal("missing terminal results")
	}
	before := len(hook.inputs)
	if _, err = p.ProcessEvent(ctx, stored); err != nil {
		t.Fatal(err)
	}
	if len(hook.inputs) != before {
		t.Fatal("terminal redelivery repeated closure")
	}
}

func TestUnknownActiveClosesBeforeKnownEventCreates(t *testing.T) {
	repo := memory.New()
	hook := &recordingHook{}
	p := newTestProcessor(t, repo, hook)
	first := persistAndProcess(t, repo, p, testEvent("opening", "critical"))
	p.severity = &changingSeverity{values: liveSeverityTable{"warning": 1}, digest: "new"}
	result := persistAndProcess(t, repo, p, testEvent("next", "warning"))
	if result.EventState != domain.EventProcessStateAccepted || len(result.AlertIDs) != 1 || result.AlertIDs[0] == first.AlertIDs[0] {
		t.Fatalf("result=%+v", result)
	}
	if len(hook.inputs) != 3 || hook.inputs[1].Alert.Status != domain.AlertStatusClosed || hook.inputs[2].Alert.Status != domain.AlertStatusActive {
		t.Fatal("closure must precede new active hook")
	}
}

type failCloseLogs struct {
	store.Repository
	fail bool
}

func (r *failCloseLogs) AppendAlertLogs(ctx context.Context, logs []domain.AlertLog) ([]store.AppendAlertLogItemResult, error) {
	if r.fail {
		r.fail = false
		return nil, errors.New("injected log failure")
	}
	return r.Repository.AppendAlertLogs(ctx, logs)
}

func TestUnknownActiveClosureRecoversPartialSuccess(t *testing.T) {
	base := memory.New()
	repo := &failCloseLogs{Repository: base}
	p := newTestProcessor(t, repo, NoopFinalHook{})
	first := persistAndProcess(t, repo, p, testEvent("opening", "critical"))
	p.severity = &changingSeverity{values: liveSeverityTable{"warning": 1}, digest: "new"}
	event := testEvent("unknown", "critical")
	input, err := repo.CreateEvent(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	repo.fail = true
	if _, err = p.ProcessEvent(context.Background(), input.StoredEvent); err == nil {
		t.Fatal("expected failure after alert close")
	}
	closed, err := repo.GetAlert(context.Background(), event.BKTenantID, first.AlertIDs[0])
	if err != nil || closed.Alert.Status != domain.AlertStatusClosed {
		t.Fatal("closure not applied")
	}
	result, err := p.ProcessEvent(context.Background(), mustGetStoredEvent(t, repo, event))
	if err != nil {
		t.Fatal(err)
	}
	if result.EventState != domain.EventProcessStateRejected {
		t.Fatal("retry did not settle event")
	}
}
