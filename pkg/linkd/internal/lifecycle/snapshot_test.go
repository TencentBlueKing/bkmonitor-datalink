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
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type snapshotRepository struct {
	store.Repository
	eventReads, enrichmentWrites, alertWrites int
	stages                                    []string
	lastVersion                               store.VersionToken
	concurrentCompletion                      bool
}

func (r *snapshotRepository) GetEvent(ctx context.Context, tenant, id string) (store.StoredEvent, error) {
	r.eventReads++
	return r.Repository.GetEvent(ctx, tenant, id)
}

func (r *snapshotRepository) CompareAndSetEventEnrichmentFromSnapshot(ctx context.Context, current store.StoredEvent, result domain.EventEnrichment) (store.StoredEvent, error) {
	r.enrichmentWrites++
	r.lastVersion = current.Version
	updated, err := r.CompareAndSetEventEnrichment(ctx, current.Event.BKTenantID, current.Event.EventID, current.Version, result)
	if err == nil {
		r.lastVersion = updated.Version
	}
	return updated, err
}

func (r *snapshotRepository) CompareAndSetLifecycleEventResultFromSnapshot(ctx context.Context, current store.StoredEvent, result store.EventResult) (store.StoredEvent, error) {
	if current.Version != r.lastVersion {
		return store.StoredEvent{}, errors.New("caller reused an earlier event version")
	}
	switch {
	case result.PolicyContext != nil:
		r.stages = append(r.stages, "policy")
	case result.Plan != nil:
		r.stages = append(r.stages, "plan")
	default:
		r.stages = append(r.stages, "final")
	}
	if r.concurrentCompletion {
		r.concurrentCompletion = false
		_, err := r.CompareAndSetEventResult(ctx, current.Event.BKTenantID, current.Event.EventID, current.Version, store.EventResult{State: domain.EventProcessStateRejected, Outcome: string(OutcomeRejected), ProcessedAt: current.Event.ReceivedAt.Add(time.Second)})
		if err != nil {
			return store.StoredEvent{}, err
		}
	}
	updated, err := r.CompareAndSetEventResult(ctx, current.Event.BKTenantID, current.Event.EventID, current.Version, result)
	if err == nil {
		r.lastVersion = updated.Version
	}
	return updated, err
}

func (r *snapshotRepository) CompareAndSetAlertFromSnapshot(ctx context.Context, current store.StoredAlert, replacement domain.Alert) (store.StoredAlert, error) {
	r.alertWrites++
	return r.CompareAndSetAlert(ctx, current.Alert.BKTenantID, current.Alert.AlertID, current.Version, replacement)
}

func TestSnapshotProcessorUsesReturnedVersionsAndKeepsThreeStateCommits(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "conflicting_owner"}[conflict], func(t *testing.T) {
			base := memory.New()
			repo := &snapshotRepository{Repository: base, concurrentCompletion: conflict}
			event := storetest.Event("tenant", "new-event", "fp", "warning")
			_, err := base.CreateEvent(t.Context(), event)
			if err != nil {
				t.Fatal(err)
			}
			p := newTestProcessor(t, repo, NoopFinalHook{})
			p.policies = snapshotterFunc(func(_ context.Context, _ domain.Event, at time.Time) (*store.PolicyContext, error) {
				return &store.PolicyContext{EvaluatedAt: at}, nil
			})
			// Scheduler 的首次读取传入处理器；之后正常路径应只消费每次提交返回的新快照。
			first, err := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
			if err != nil {
				t.Fatal(err)
			}
			result, err := p.ProcessEvent(t.Context(), first)
			if err != nil {
				t.Fatal(err)
			}
			if repo.enrichmentWrites != 1 {
				t.Fatal("enrichment repeated")
			}
			if conflict {
				if repo.eventReads != 2 || result.EventState != domain.EventProcessStateRejected || !reflect.DeepEqual(repo.stages, []string{"policy"}) {
					t.Fatalf("conflict did not reload completed event: reads=%d stages=%v state=%s", repo.eventReads, repo.stages, result.EventState)
				}
			} else {
				if repo.eventReads != 1 || result.EventState != domain.EventProcessStateAccepted || !reflect.DeepEqual(repo.stages, []string{"policy", "plan", "final"}) {
					t.Fatalf("normal snapshot chain: reads=%d stages=%v state=%s", repo.eventReads, repo.stages, result.EventState)
				}
				next := storetest.Event("tenant", "next-event", "fp", "warning")
				created, err := base.CreateEvent(t.Context(), next)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := p.ProcessEvent(t.Context(), created.StoredEvent); err != nil {
					t.Fatal(err)
				}
				if repo.alertWrites != 1 {
					t.Fatalf("Alert CAS did not use snapshot path: %d", repo.alertWrites)
				}
			}
		})
	}
}
