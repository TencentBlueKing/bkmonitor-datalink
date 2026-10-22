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
	"linkd/internal/store"
	"linkd/internal/store/memory"
)

func TestClosePreparationFailureKeepsActiveAndRetryDoesNotRepeatAfterClose(t *testing.T) {
	repo := memory.New()
	p := newTestProcessor(t, repo, NoopFinalHook{})
	event := testEvent("opening", "warning")
	created := persistAndProcess(t, repo, p, event)
	command := CloseAlertCommand{BKTenantID: event.BKTenantID, AlertID: created.AlertIDs[0], OperationID: "close", OperatorKind: domain.OperatorKindUser, OperatorID: "user", Reason: "manual", EffectiveAt: event.ReceivedAt.Add(time.Hour)}
	calls := 0
	dependencyErr := errors.New("alarmd unavailable")
	WithClosePreparation(func(ctx context.Context, alert domain.Alert, operation domain.AlertEndOperation) error {
		calls++
		stored, err := repo.GetAlert(ctx, command.BKTenantID, command.AlertID)
		if err != nil || stored.Alert.Status != domain.AlertStatusActive || alert.SourceAlertID != event.SourceAlertID || operation.Source != "manual" {
			t.Fatalf("preparation must precede close: %+v %v", stored, err)
		}
		return dependencyErr
	})(p)
	if _, err := p.CloseAlert(t.Context(), command); !errors.Is(err, dependencyErr) {
		t.Fatalf("failure = %v", err)
	}
	stored, err := repo.GetAlert(t.Context(), command.BKTenantID, command.AlertID)
	if err != nil || stored.Alert.Status != domain.AlertStatusActive || calls != 1 {
		t.Fatalf("failed preparation changed alert: %+v %v calls=%d", stored, err, calls)
	}
	dependencyErr = nil
	if _, err := p.CloseAlert(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	result, err := p.CloseAlert(t.Context(), command)
	if err != nil || !result.AlreadyClosed || calls != 2 {
		t.Fatalf("terminal retry repeated preparation: %+v %v calls=%d", result, err, calls)
	}
	command.OperationID = "other"
	if _, err := p.CloseAlert(t.Context(), command); !errors.Is(err, store.ErrInvalidTransition) || calls != 2 {
		t.Fatalf("terminal conflict ran preparation: %v calls=%d", err, calls)
	}
}

type closeWriteFailureRepository struct {
	store.Repository
	failure error
}

func (r *closeWriteFailureRepository) CompareAndSetAlert(ctx context.Context, tenant, id string, version store.VersionToken, alert domain.Alert) (store.StoredAlert, error) {
	if r.failure != nil {
		err := r.failure
		r.failure = nil
		return store.StoredAlert{}, err
	}
	return r.Repository.CompareAndSetAlert(ctx, tenant, id, version, alert)
}

func TestClosePreparationRunsOncePerAttemptDespiteCASRetry(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "write failure needs another request", true: "CAS retry stays in same request"}[conflict], func(t *testing.T) {
			repo := &closeWriteFailureRepository{Repository: memory.New()}
			p := newTestProcessor(t, repo, NoopFinalHook{})
			event := testEvent("opening", "warning")
			created := persistAndProcess(t, repo, p, event)
			command := CloseAlertCommand{BKTenantID: event.BKTenantID, AlertID: created.AlertIDs[0], OperationID: "close", OperatorKind: domain.OperatorKindUser, OperatorID: "user", Reason: "manual", EffectiveAt: event.ReceivedAt.Add(time.Hour)}
			calls := 0
			WithClosePreparation(func(context.Context, domain.Alert, domain.AlertEndOperation) error { calls++; return nil })(p)
			repo.failure = errors.New("store unavailable")
			if conflict {
				repo.failure = store.ErrVersionConflict
			}
			result, err := p.CloseAlert(t.Context(), command)
			if calls != 1 {
				t.Fatalf("one request notified %d times", calls)
			}
			if conflict {
				if err != nil || result.Alert.Status != domain.AlertStatusClosed {
					t.Fatalf("CAS retry failed: %+v %v", result, err)
				}
			} else {
				if err == nil {
					t.Fatal("write failure ignored")
				}
				stored, readErr := repo.GetAlert(t.Context(), command.BKTenantID, command.AlertID)
				if readErr != nil || stored.Alert.Status != domain.AlertStatusActive {
					t.Fatalf("write failure = %+v %v", stored, readErr)
				}
				if _, err := p.CloseAlert(t.Context(), command); err != nil || calls != 2 {
					t.Fatalf("retry = %v calls=%d", err, calls)
				}
			}
		})
	}
}

func TestSourceTerminalAndSeverityRotationDoNotPrepareDirectClose(t *testing.T) {
	for _, kind := range []string{"recovered", "closed", "severity_upgrade"} {
		t.Run(kind, func(t *testing.T) {
			repo := memory.New()
			p := newTestProcessor(t, repo, NoopFinalHook{})
			WithClosePreparation(func(context.Context, domain.Alert, domain.AlertEndOperation) error {
				t.Fatal("source event invoked direct close preparation")
				return nil
			})(p)
			persistAndProcess(t, repo, p, testEvent("opening", "warning"))
			event := testEvent("next", "warning")
			switch kind {
			case "recovered":
				event.Evaluations[0].Action = domain.EventActionResolved
			case "closed":
				event.Evaluations[0].Action = domain.EventActionClosed
			default:
				event.Evaluations[0].Severity = "critical"
			}
			persistAndProcess(t, repo, p, event)
		})
	}
}
