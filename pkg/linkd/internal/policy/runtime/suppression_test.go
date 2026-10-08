// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/onemodel"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
	"linkd/internal/suppressioncleanup"
)

type suppressionReleaseReader struct {
	release policy.Release
	err     error
	calls   atomic.Int64
}

func (r *suppressionReleaseReader) GetRelease(_ context.Context, scope policy.Scope, id string, version int64) (policy.Release, error) {
	r.calls.Add(1)
	if r.err != nil {
		return policy.Release{}, r.err
	}
	if r.release.Scope != scope || r.release.ID != id || r.release.Version != version {
		return policy.Release{}, policy.ErrNotFound
	}
	return r.release, nil
}

type suppressionTargets struct {
	err     error
	foreign bool
}

func (r suppressionTargets) ResolveScope(_ context.Context, tenant, space string) (onemodel.TargetScope, error) {
	if r.foreign {
		tenant = "foreign"
	}
	return onemodel.TargetScope{TenantID: tenant, SpaceCode: space, BusinessIDs: []int64{2}}, r.err
}

func (r suppressionTargets) Resolve(context.Context, string, string, onemodel.TargetDescriptor) (onemodel.TargetResult, error) {
	return onemodel.TargetResult{}, errors.New("unexpected target descriptor")
}

type suppressionState struct {
	requests []redisstate.ClipRequest
	binds    []string
	clears   []string
	err      error
	decision redisstate.ClipDecision
}

func (s *suppressionState) Clip(_ context.Context, r redisstate.ClipRequest) (redisstate.ClipDecision, error) {
	s.requests = append(s.requests, r)
	return s.decision, s.err
}

func (s *suppressionState) BindClipOwner(_ context.Context, _ redisstate.ClipRequest, _ redisstate.ClipDecision, id string) error {
	s.binds = append(s.binds, id)
	return s.err
}

func (s *suppressionState) ClearClipIdentity(_ context.Context, _ redisstate.Identity, owner string) ([]suppressioncleanup.Window, error) {
	s.clears = append(s.clears, owner)
	return []suppressioncleanup.Window{{ID: strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64), Epoch: "opening"}}, s.err
}

type suppressionObserver struct{ outcomes []string }

func (o *suppressionObserver) ObserveSuppression(_ context.Context, scheme, outcome, reason string) {
	o.outcomes = append(o.outcomes, scheme+":"+outcome+":"+reason)
}

func clipRelease(t *testing.T) policy.Release {
	t.Helper()
	raw := json.RawMessage(`{"name":"clip","is_enable":true,"updated_at":"2026-09-30T00:00:00Z","space_code":"bkcc__2","activate_times":[{"period":"everyday","open_clock_time":"00:00:00","close_clock_time":"23:59:59"}],"policy":{"expression":"A","A":{"condition":"term","target_key":"source_id","target_value":"source"}},"scheme":[{"type":"clip","count":3,"duration":60,"duration_type":"second"}]}`)
	compiled, err := policy.Compile(policy.Suppression, raw)
	if err != nil {
		t.Fatal(err)
	}
	return policy.Release{Scope: policy.Scope{TenantID: "tenant", Kind: policy.Suppression}, ID: "clip", Version: 1, Spec: compiled.Canonical, Compiled: compiled.Summary}
}

func suppressionFixture(t *testing.T) (*Suppressor, domain.Event, *store.PolicyContext, *suppressionState) {
	t.Helper()
	release := clipRelease(t)
	event := storetest.Event("tenant", "event", "fp", "warning")
	event.Dimensions["bk_biz_id"] = domain.NewStringScalar("2")
	event.Labels["source_id"] = domain.NewStringScalar("source")
	var err error
	event, err = event.WithEnrichment(storetest.Enrichment(event))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &store.PolicyContext{EvaluatedAt: event.CreateAt.Add(7 * time.Hour), Releases: []store.PolicyReleaseRef{{Kind: "suppression", ID: release.ID, Version: release.Version, Digest: release.Compiled.Digest}}}
	state := &suppressionState{decision: redisstate.ClipDecision{Count: 1, Threshold: 3, CounterID: strings.Repeat("b", 64), Epoch: event.EventID, EvaluatedAtMillis: snapshot.EvaluatedAt.UnixMilli()}}
	return &Suppressor{Releases: &suppressionReleaseReader{release: release}, Targets: suppressionTargets{}, State: state}, event, snapshot, state
}

func TestSuppressionUsesFrozenReleaseTimeAndSavedEnrich(t *testing.T) {
	s, e, snapshot, state := suppressionFixture(t)
	result, err := s.Check(t.Context(), e, e.Evaluations[0], snapshot, nil)
	if err != nil || !result.Suppressed || result.ReasonCode != "clip_below_threshold" || len(state.requests) != 1 {
		t.Fatalf("clip not applied %+v %v", result, err)
	}
	if state.requests[0].At != snapshot.EvaluatedAt || state.requests[0].Version != 1 || state.requests[0].EventID != e.EventID {
		t.Fatal("used source time or latest version")
	}
	d := &store.PolicyDecision{Suppression: &store.SuppressionDecision{Evaluations: []store.SuppressionEvaluation{result}}}
	if err := store.ValidatePolicyDecision(e, snapshot, d); err != nil {
		t.Fatal(err)
	}
	// 新发布版本不会成为旧引用的替代。精确版本不存在时记录跳过，绝不读新配置裁决。
	reader := s.Releases.(*suppressionReleaseReader)
	reader.release.Version = 2
	result, err = s.Check(t.Context(), e, e.Evaluations[0], snapshot, nil)
	if err != nil || result.Suppressed || result.Steps[0].ReasonCode != "release_unavailable" || len(state.requests) != 1 {
		t.Fatalf("latest version substituted %+v %v", result, err)
	}
}

func TestSuppressionErrorsRemainDistinctFromBusinessDecisions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Suppressor, *domain.Event, *suppressionState)
		hard   bool
	}{
		{"redis", func(_ *Suppressor, _ *domain.Event, s *suppressionState) { s.err = errors.New("private redis error") }, false},
		{"metadata", func(s *Suppressor, _ *domain.Event, _ *suppressionState) {
			s.Targets = suppressionTargets{err: errors.New("down")}
		}, false},
		{"tenant", func(s *Suppressor, _ *domain.Event, _ *suppressionState) {
			s.Targets = suppressionTargets{foreign: true}
		}, true},
		{"auth", func(s *Suppressor, _ *domain.Event, _ *suppressionState) {
			s.Releases.(*suppressionReleaseReader).err = policy.ErrAccess
		}, true},
		{"digest", func(s *Suppressor, _ *domain.Event, _ *suppressionState) {
			s.Releases.(*suppressionReleaseReader).release.Compiled.Digest = strings.Repeat("f", 64)
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, e, snapshot, state := suppressionFixture(t)
			tc.change(s, &e, state)
			observer := &suppressionObserver{}
			s.Observer = observer
			result, err := s.Check(t.Context(), e, e.Evaluations[0], snapshot, nil)
			if tc.hard {
				if err == nil {
					t.Fatal("hard error became skip")
				}
				return
			}
			if err != nil || result.Suppressed || len(result.Steps) != 1 || result.Steps[0].Outcome != "skipped" || len(observer.outcomes) != 1 {
				t.Fatalf("ordinary error became business result %+v %v", result, err)
			}
			raw, _ := json.Marshal(result)
			if strings.Contains(string(raw), "private") {
				t.Fatal("backend error leaked")
			}
		})
	}
	s, e, snapshot, _ := suppressionFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Check(ctx, e, e.Evaluations[0], snapshot, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestSuppressionBindsOnlyAdmittedSelectedLevelAndTerminalCleanup(t *testing.T) {
	s, e, snapshot, state := suppressionFixture(t)
	state.decision.Allowed = true
	state.decision.Count = 3
	result, err := s.Check(t.Context(), e, e.Evaluations[0], snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := &store.PolicyDecision{Suppression: &store.SuppressionDecision{Evaluations: []store.SuppressionEvaluation{result}}}
	a := domain.Alert{Revision: 1, BKTenantID: e.BKTenantID, EventSourceID: e.EventSourceID, Fingerprint: e.Fingerprint, AlertID: "actual-alert", Severity: "warning"}
	if err := s.Bind(t.Context(), e, d, a); err != nil || len(state.binds) != 1 || state.binds[0] != a.AlertID {
		t.Fatal("owner not bound", err)
	}
	a.BKTenantID = "foreign"
	if err := s.Bind(t.Context(), e, d, a); !errors.Is(err, policy.ErrAccess) {
		t.Fatal("foreign bind accepted")
	}
	observer := &suppressionObserver{}
	s.Observer = observer
	state.err = errors.New("down")
	if err := s.Clear(t.Context(), suppressioncleanup.Cause{TenantID: e.BKTenantID, SourceID: e.EventSourceID, Fingerprint: e.Fingerprint, Trigger: "alert_terminal", AlertID: "actual-alert", Revision: 2, Status: domain.AlertStatusClosed}); err != nil || len(observer.outcomes) != 1 || state.clears[0] != "actual-alert" {
		t.Fatalf("cleanup skip lost %v %v", observer.outcomes, err)
	}
}

func TestCombinedSuppressionCountsBeforeInvalidGrouping(t *testing.T) {
	s, event, snapshot, state := suppressionFixture(t)
	reader := s.Releases.(*suppressionReleaseReader)
	var spec policy.SuppressionSpec
	if err := json.Unmarshal(reader.release.Spec, &spec); err != nil {
		t.Fatal(err)
	}
	spec.Schemes = append(spec.Schemes, policy.Scheme{Type: "aggregation", Duration: 60, DurationType: "second", Fields: []string{"model_id"}})
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := policy.Compile(policy.Suppression, raw)
	if err != nil {
		t.Fatal(err)
	}
	reader.release.Spec = compiled.Canonical
	reader.release.Compiled = compiled.Summary
	snapshot.Releases[0].Digest = compiled.Summary.Digest
	result, err := s.Check(t.Context(), event, event.Evaluations[0], snapshot, nil)
	if err != nil || !result.Suppressed || len(result.Steps) != 1 || len(state.requests) != 1 {
		t.Fatalf("invalid aggregation skipped debounce %+v %v", result, err)
	}
	state.decision.Allowed = true
	state.decision.Count = 3
	result, err = s.Check(t.Context(), event, event.Evaluations[0], snapshot, nil)
	if err != nil || result.Suppressed || len(result.Steps) != 2 || result.Steps[1].ReasonCode != "invalid_group_value" {
		t.Fatalf("grouping did not follow passed debounce %+v %v", result, err)
	}
}

type cleanupRecorderFunc func(context.Context, suppressioncleanup.Cause, func(context.Context) (suppressioncleanup.Result, error)) error

func (f cleanupRecorderFunc) Run(ctx context.Context, c suppressioncleanup.Cause, run func(context.Context) (suppressioncleanup.Result, error)) error {
	return f(ctx, c, run)
}

func TestSuppressionCleanupRecorderKeepsRedisFailureSeparateFromCoreFailure(t *testing.T) {
	s, event, _, state := suppressionFixture(t)
	cause := suppressioncleanup.Cause{TenantID: event.BKTenantID, SourceID: event.EventSourceID, Fingerprint: event.Fingerprint, Trigger: "event_terminal", EventID: "terminal"}
	state.err = errors.New("private Redis failure")
	s.CleanupRecorder = cleanupRecorderFunc(func(ctx context.Context, c suppressioncleanup.Cause, run func(context.Context) (suppressioncleanup.Result, error)) error {
		if c != cause {
			t.Fatal("cleanup cause drifted")
		}
		result, err := run(ctx)
		if err != nil || result.Clip.State != "unavailable" || result.Clip.Removed != nil || result.Aggregation.State != "not_applicable" {
			t.Fatal("Redis failure became zero or stopped terminal", result, err)
		}
		return err
	})
	if err := s.Clear(t.Context(), cause); err != nil {
		t.Fatal(err)
	}
	state.clears = nil
	coreErr := errors.New("audit store failed")
	s.CleanupRecorder = cleanupRecorderFunc(func(context.Context, suppressioncleanup.Cause, func(context.Context) (suppressioncleanup.Result, error)) error {
		return coreErr
	})
	if err := s.Clear(t.Context(), cause); !errors.Is(err, coreErr) || len(state.clears) != 0 {
		t.Fatal("core storage failure was swallowed or Redis ran", err)
	}
}
