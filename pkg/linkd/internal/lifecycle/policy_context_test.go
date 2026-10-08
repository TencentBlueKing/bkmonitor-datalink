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
)

type snapshotterFunc func(context.Context, domain.Event, time.Time) (*store.PolicyContext, error)

func (f snapshotterFunc) Snapshot(ctx context.Context, event domain.Event, at time.Time) (*store.PolicyContext, error) {
	return f(ctx, event, at)
}

type failPolicyPlanOnce struct {
	store.Repository
	failed bool
}

func (r *failPolicyPlanOnce) CompareAndSetEventResult(ctx context.Context, tenant, id string, version store.VersionToken, result store.EventResult) (store.StoredEvent, error) {
	if result.Plan != nil && !r.failed {
		r.failed = true
		return store.StoredEvent{}, errors.New("plan unavailable")
	}
	return r.Repository.CompareAndSetEventResult(ctx, tenant, id, version, result)
}

func TestPolicyContextSurvivesPlanFailureAndDoesNotReload(t *testing.T) {
	repo := &failPolicyPlanOnce{Repository: memory.New()}
	p := newTestProcessor(t, repo, &recordingHook{})
	calls := 0
	p.policies = snapshotterFunc(func(ctx context.Context, event domain.Event, at time.Time) (*store.PolicyContext, error) {
		calls++
		if event.EnrichStatus == domain.EnrichStatusPending {
			t.Fatal("snapshot before enrich")
		}
		saved, err := repo.GetEvent(ctx, event.BKTenantID, event.EventID)
		if err != nil || saved.Event.EnrichStatus == domain.EnrichStatusPending || saved.Processing.Plan != nil {
			t.Fatal("snapshot ordering invalid")
		}
		return &store.PolicyContext{EvaluatedAt: at, Releases: []store.PolicyReleaseRef{{Kind: "suppression", ID: "p1", Version: int64(calls), Digest: strings.Repeat("a", 64)}}}, nil
	})
	event := testEvent("policy-retry", "warning")
	created, err := repo.CreateEvent(t.Context(), event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ProcessEvent(t.Context(), created.StoredEvent); err == nil {
		t.Fatal("expected first plan failure")
	}
	frozen, err := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
	if err != nil || frozen.Processing.PolicyContext == nil || frozen.Processing.Plan != nil {
		t.Fatalf("context not committed before plan %+v %v", frozen.Processing, err)
	}
	result, err := p.ProcessEvent(t.Context(), frozen)
	if err != nil || len(result.AlertIDs) != 1 || calls != 1 {
		t.Fatalf("retry reloaded policies %+v calls=%d %v", result, calls, err)
	}
	saved, err := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
	if err != nil || !reflect.DeepEqual(saved.Processing.PolicyContext, frozen.Processing.PolicyContext) {
		t.Fatal("final result lost policy context")
	}
	if _, err := p.ProcessEvent(t.Context(), saved); err != nil || calls != 1 {
		t.Fatal("completed replay reloaded configuration")
	}
}

func TestPolicyContextErrorAndRecordedSkip(t *testing.T) {
	denied := errors.New("access denied")
	for _, hard := range []bool{false, true} {
		repo := memory.New()
		p := newTestProcessor(t, repo, &recordingHook{})
		p.policies = snapshotterFunc(func(_ context.Context, _ domain.Event, at time.Time) (*store.PolicyContext, error) {
			if hard {
				return nil, denied
			}
			return &store.PolicyContext{EvaluatedAt: at, Releases: []store.PolicyReleaseRef{}, ReasonCode: "policy_load_failed"}, nil
		})
		event := testEvent("policy-error", "warning")
		created, err := repo.CreateEvent(t.Context(), event)
		if err != nil {
			t.Fatal(err)
		}
		result, err := p.ProcessEvent(t.Context(), created.StoredEvent)
		if hard {
			if !errors.Is(err, denied) || len(result.AlertIDs) != 0 {
				t.Fatalf("hard error swallowed %v", err)
			}
		} else {
			if err != nil || len(result.AlertIDs) != 1 {
				t.Fatalf("dependency skip blocked event %v", err)
			}
			saved, _ := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
			if saved.Processing.PolicyContext.ReasonCode != "policy_load_failed" {
				t.Fatal("skip reason lost")
			}
		}
	}
}
