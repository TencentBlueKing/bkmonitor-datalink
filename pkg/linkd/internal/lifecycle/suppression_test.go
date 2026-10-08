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
	"reflect"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/suppressioncleanup"
)

type testSuppressor struct {
	checks []string
	binds  []string
	clears []string
	check  func(domain.EventEvaluation) (store.SuppressionEvaluation, error)
}

func (s *testSuppressor) Check(_ context.Context, _ domain.Event, e domain.EventEvaluation, _ *store.PolicyContext, _ func(string) (string, error)) (store.SuppressionEvaluation, error) {
	s.checks = append(s.checks, e.Severity)
	return s.check(e)
}

func (s *testSuppressor) Bind(_ context.Context, _ domain.Event, _ *store.PolicyDecision, a domain.Alert) error {
	s.binds = append(s.binds, a.AlertID)
	return nil
}

func (s *testSuppressor) Clear(_ context.Context, c suppressioncleanup.Cause) error {
	s.clears = append(s.clears, c.AlertID)
	return nil
}

func suppressionTestSnapshot() *store.PolicyContext {
	return &store.PolicyContext{EvaluatedAt: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), Releases: []store.PolicyReleaseRef{{Kind: "suppression", ID: "clip", Version: 1, Digest: strings.Repeat("a", 64)}}}
}

func belowThreshold(e domain.EventEvaluation) (store.SuppressionEvaluation, error) {
	return store.SuppressionEvaluation{Severity: e.Severity, Suppressed: true, ReasonCode: "clip_below_threshold", Steps: []store.SuppressionStep{{Policy: suppressionTestSnapshot().Releases[0], Scheme: "clip", Outcome: "suppressed", ReasonCode: "clip_below_threshold", Count: 1, Threshold: 3, DurationSeconds: 60, EvaluatedAtMillis: suppressionTestSnapshot().EvaluatedAt.UnixMilli(), CounterID: strings.Repeat("b", 64), Epoch: "first-event"}}}, nil
}

func withSuppression(p *Processor, s *testSuppressor) {
	p.suppressor = s
	p.policies = snapshotterFunc(func(context.Context, domain.Event, time.Time) (*store.PolicyContext, error) {
		return suppressionTestSnapshot(), nil
	})
}

func TestActiveAlertBypassesNewAlertSuppression(t *testing.T) {
	for _, tc := range []struct {
		name, opening, policy, want string
		evaluations                 []domain.EventEvaluation
	}{
		{"repeat", "warning", "update_current", "warning", []domain.EventEvaluation{{Severity: "warning", Action: domain.EventActionTriggered}}},
		{"lower", "critical", "update_current", "critical", []domain.EventEvaluation{{Severity: "warning", Action: domain.EventActionTriggered}}},
		{"upgrade in place", "warning", "update_current", "critical", []domain.EventEvaluation{{Severity: "critical", Action: domain.EventActionTriggered}}},
		{"upgrade rotates", "warning", "close_and_create", "critical", []domain.EventEvaluation{{Severity: "critical", Action: domain.EventActionTriggered}}},
		{"upgrade supersedes recovery", "warning", "close_and_create", "critical", []domain.EventEvaluation{{Severity: "warning", Action: domain.EventActionResolved}, {Severity: "critical", Action: domain.EventActionTriggered}}},
		{"recover and lower trigger", "critical", "update_current", "warning", []domain.EventEvaluation{{Severity: "critical", Action: domain.EventActionResolved}, {Severity: "warning", Action: domain.EventActionTriggered}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := memory.New()
			p := newTestProcessor(t, repo, &recordingHook{})
			p.upgradePolicy = tc.policy
			opening := persistAndProcess(t, repo, p, testEvent("opening", tc.opening))
			s := &testSuppressor{check: belowThreshold}
			withSuppression(p, s)
			next := testEvent("next", "warning")
			next.Evaluations = tc.evaluations
			result := persistAndProcess(t, repo, p, next)
			if len(s.checks) != 0 {
				t.Fatalf("active alert ran clip/aggregation: %v", s.checks)
			}
			active, err := repo.FindActiveAlert(t.Context(), activeKeyForTest(next))
			if err != nil || active.Alert.Severity != tc.want {
				t.Fatalf("lost lifecycle transition %v %v", active, err)
			}
			saved, err := repo.GetEvent(t.Context(), next.BKTenantID, next.EventID)
			if err != nil {
				t.Fatal(err)
			}
			d := saved.Processing.PolicyDecision
			if d == nil || d.Suppression.BypassReason != "active_alert" || d.Suppression.ActiveAlertID != opening.AlertIDs[0] || len(d.Suppression.Evaluations) != 0 {
				t.Fatalf("missing bypass diagnostic %+v result=%+v", d, result)
			}
			// 已完成 Event 重投不会重新尝试门槛或状态清理。
			before := len(s.clears)
			if _, err := p.ProcessEvent(t.Context(), saved); err != nil || len(s.checks) != 0 || len(s.clears) != before {
				t.Fatalf("replay repeated suppression %v", err)
			}
		})
	}
}

func TestNewAlertSuppressionPersistsWithoutAlertAndRecoveryClears(t *testing.T) {
	repo := memory.New()
	hook := &recordingHook{}
	p := newTestProcessor(t, repo, hook)
	s := &testSuppressor{check: belowThreshold}
	withSuppression(p, s)
	event := testEvent("new", "warning")
	result := persistAndProcess(t, repo, p, event)
	if result.EventState != domain.EventProcessStateSuppressed || result.ReasonCode != "clip_below_threshold" || len(result.AlertIDs) != 0 || len(hook.inputs) != 0 || len(s.binds) != 0 {
		t.Fatalf("unexpected suppressed result %+v", result)
	}
	saved, err := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Processing.Plan != nil || saved.Processing.PolicyDecision.Suppression.Evaluations[0].Steps[0].Count != 1 {
		t.Fatalf("lost committed decision %+v", saved.Processing)
	}
	end := testEvent("no-active-recovery", "warning")
	end.Evaluations[0].Action = domain.EventActionResolved
	ended := persistAndProcess(t, repo, p, end)
	if ended.EventState != domain.EventProcessStateOrphaned || !reflect.DeepEqual(s.clears, []string{""}) || len(s.checks) != 1 {
		t.Fatalf("orphan terminal did not clean %v %+v", s, ended)
	}
}

func TestSuppressedHighLevelDoesNotDiscardLowerCandidate(t *testing.T) {
	repo := memory.New()
	p := newTestProcessor(t, repo, &recordingHook{})
	s := &testSuppressor{check: func(e domain.EventEvaluation) (store.SuppressionEvaluation, error) {
		if e.Severity == "critical" {
			return belowThreshold(e)
		}
		return store.SuppressionEvaluation{Severity: e.Severity}, nil
	}}
	withSuppression(p, s)
	event := testEvent("multiple", "warning")
	event.Evaluations = append(event.Evaluations, domain.EventEvaluation{Severity: "critical", Action: domain.EventActionTriggered})
	result := persistAndProcess(t, repo, p, event)
	active, err := repo.FindActiveAlert(t.Context(), activeKeyForTest(event))
	if err != nil || active.Alert.Severity != "warning" || len(result.AlertIDs) != 1 || !reflect.DeepEqual(s.checks, []string{"critical", "warning"}) {
		t.Fatalf("per-evaluation admission failed %v %+v %v", s.checks, active, err)
	}
	saved, _ := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
	for _, e := range saved.Processing.Evaluations {
		if e.Severity == "critical" && e.ReasonCode != "clip_below_threshold" {
			t.Fatalf("lost high-level suppression %+v", e)
		}
		if e.Severity == "warning" && e.State != domain.EventProcessStateAccepted {
			t.Fatalf("lost lower-level admission %+v", e)
		}
	}
}

func TestSuppressionDecisionResumesSavedPlanWithoutCallingRedis(t *testing.T) {
	repo := &failNextCreateRepository{Repository: memory.New()}
	p := newTestProcessor(t, repo, &recordingHook{})
	s := &testSuppressor{check: func(e domain.EventEvaluation) (store.SuppressionEvaluation, error) {
		return store.SuppressionEvaluation{Severity: e.Severity}, nil
	}}
	withSuppression(p, s)
	event := testEvent("saved-plan", "warning")
	created, err := repo.CreateEvent(t.Context(), event)
	if err != nil {
		t.Fatal(err)
	}
	repo.fail = true
	if _, err := p.ProcessEvent(t.Context(), created.StoredEvent); err == nil {
		t.Fatal("expected create failure")
	}
	pending, _ := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
	if pending.Processing.Plan == nil || pending.Processing.Plan.PolicyDecision == nil {
		t.Fatal("decision not frozen")
	}
	s.check = func(domain.EventEvaluation) (store.SuppressionEvaluation, error) {
		return store.SuppressionEvaluation{}, errors.New("must not reevaluate")
	}
	if _, err := p.ProcessEvent(t.Context(), pending); err != nil || len(s.checks) != 1 {
		t.Fatalf("saved plan re-evaluated %v", err)
	}
}
