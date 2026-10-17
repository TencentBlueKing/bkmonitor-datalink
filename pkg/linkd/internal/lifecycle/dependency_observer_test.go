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
	"time"

	"linkd/internal/domain"
	"linkd/internal/processingerror"
	"linkd/internal/store"
	"linkd/internal/store/memory"
)

type recordingDependencyIndex struct {
	alerts []domain.Alert
	err    error
}

type terminalDependencyIndex struct {
	recordingDependencyIndex
	failClear bool
}

func (o *terminalDependencyIndex) ObserveDependencyAlert(_ context.Context, a domain.Alert, _ func(string) (string, error)) error {
	o.alerts = append(o.alerts, a.Clone())
	if a.Status == domain.AlertStatusActive {
		return errors.New("target dependency unavailable")
	}
	if o.failClear {
		o.failClear = false
		return errors.New("index clear temporarily unavailable")
	}
	return nil
}

func TestTerminalMutationDoesNotRequireOldDependencyQualification(t *testing.T) {
	for _, action := range []domain.EventAction{domain.EventActionResolved, domain.EventActionClosed} {
		for _, failClear := range []bool{false, true} {
			t.Run(string(action)+map[bool]string{false: "", true: "/clear-retry"}[failClear], func(t *testing.T) {
				repo := memory.New()
				p := newTestProcessor(t, repo, NoopFinalHook{})
				opening := testEvent("opening", "warning")
				result := persistAndProcess(t, repo, p, opening)
				index := &terminalDependencyIndex{failClear: failClear}
				p.dependency = index
				event := testEvent("terminal", "warning")
				event.Evaluations[0].Action = action
				saved, err := repo.CreateEvent(t.Context(), event)
				if err != nil {
					t.Fatal(err)
				}
				_, err = p.ProcessEvent(t.Context(), saved.StoredEvent)
				current, readErr := repo.GetAlert(t.Context(), opening.BKTenantID, result.AlertIDs[0])
				if readErr != nil || !current.Alert.Status.Terminal() {
					t.Fatalf("dependency outage prevented terminal CAS: %v, %v", err, readErr)
				}
				if failClear {
					if !processingerror.Retryable(err) {
						t.Fatalf("clear failure not retryable: %v", err)
					}
					_, err = p.ProcessEvent(t.Context(), mustGetStoredEvent(t, repo, event))
				}
				if err != nil {
					t.Fatal(err)
				}
				for _, observed := range index.alerts {
					if !observed.Status.Terminal() {
						t.Fatal("requalified old active alert")
					}
				}
			})
		}
	}
}

func TestDirectCloseDoesNotRequireOldDependencyQualification(t *testing.T) {
	repo := memory.New()
	p := newTestProcessor(t, repo, NoopFinalHook{})
	event := testEvent("opening", "warning")
	result := persistAndProcess(t, repo, p, event)
	p.dependency = &terminalDependencyIndex{failClear: true}
	command := CloseAlertCommand{BKTenantID: event.BKTenantID, AlertID: result.AlertIDs[0], OperationID: "close", OperatorKind: domain.OperatorKindUser, OperatorID: "user", Reason: "manual", EffectiveAt: event.ReceivedAt.Add(time.Hour)}
	if _, err := p.CloseAlert(t.Context(), command); !processingerror.Retryable(err) {
		t.Fatalf("expected clear retry: %v", err)
	}
	current, err := repo.GetAlert(t.Context(), event.BKTenantID, result.AlertIDs[0])
	if err != nil || current.Alert.Status != domain.AlertStatusClosed {
		t.Fatal("close blocked before CAS", err)
	}
	if _, err := p.CloseAlert(t.Context(), command); err != nil {
		t.Fatal(err)
	}
}

func (*recordingDependencyIndex) BindDependency(context.Context, domain.Event, *store.PolicyDecision, domain.Alert) error {
	return nil
}

func (*recordingDependencyIndex) ClearDependency(context.Context, string, string) error { return nil }

func (o *recordingDependencyIndex) ObserveDependencyAlert(_ context.Context, a domain.Alert, _ func(string) (string, error)) error {
	o.alerts = append(o.alerts, a.Clone())
	return o.err
}

func TestDependencyIndexFailureAfterCreateCanRepairFromCurrentState(t *testing.T) {
	repo := memory.New()
	p := newTestProcessor(t, repo, NoopFinalHook{})
	index := &recordingDependencyIndex{err: errors.New("index temporarily unavailable")}
	p.dependency = index
	alert := storetestAlert(testEvent("opening", "warning"), "observer-alert")
	created, err := p.createAlertAfterActiveLookup(t.Context(), alert)
	if !processingerror.Retryable(err) || !created.Created {
		t.Fatalf("created=%v error=%v", created.Created, err)
	}
	stored, err := repo.GetAlert(t.Context(), alert.BKTenantID, alert.AlertID)
	if err != nil || stored.Alert.Revision != 1 {
		t.Fatalf("committed alert missing: %v", err)
	}
	index.err = nil
	repaired, err := p.getAlertCurrent(t.Context(), alert.BKTenantID, alert.AlertID)
	if err != nil || repaired.Alert.Revision != 1 || len(index.alerts) != 2 {
		t.Fatalf("repair error=%v observations=%d", err, len(index.alerts))
	}
	if index.alerts[0].AlertID != index.alerts[1].AlertID {
		t.Fatal("repair observed a different alert")
	}
}

func TestDependencyIndexObservesCommittedUpdatesAndTerminalState(t *testing.T) {
	repo := memory.New()
	p := newTestProcessor(t, repo, NoopFinalHook{})
	index := &recordingDependencyIndex{}
	p.dependency = index
	alert := storetestAlert(testEvent("opening", "warning"), "observer-terminal")
	created, err := p.createAlertAfterActiveLookup(t.Context(), alert)
	if err != nil {
		t.Fatal(err)
	}
	replacement := created.Alert.Clone()
	replacement.Status = domain.AlertStatusRecovered
	replacement.EndType = domain.AlertEndTypeSource
	replacement.UpdateAt = replacement.UpdateAt.Add(time.Second)
	replacement.EndAt = &replacement.UpdateAt
	updated, err := p.compareAndSetAlert(t.Context(), created.StoredAlert, replacement)
	if err != nil {
		t.Fatal(err)
	}
	if len(index.alerts) != 2 || index.alerts[1].Revision != updated.Alert.Revision || index.alerts[1].Status != domain.AlertStatusRecovered {
		t.Fatalf("observations=%+v", index.alerts)
	}
}
