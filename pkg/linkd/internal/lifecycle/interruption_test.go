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
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/memory"
)

// 故障只注入被测步骤；已提交后取消 context 模拟进程退出而非业务失败重试。
type interruptionRepository struct {
	store.Repository
	stage        string
	after, fired bool
	cancel       context.CancelFunc
}

func (r *interruptionRepository) interrupt(stage string, after bool) error {
	if r.fired || r.stage != stage || r.after != after {
		return nil
	}
	r.fired = true
	if after {
		r.cancel()
		return context.Canceled
	}
	return errors.New("injected step failure")
}

func (r *interruptionRepository) CreateAlert(ctx context.Context, a domain.Alert) (store.CreateAlertResult, error) {
	if err := r.interrupt("create", false); err != nil {
		return store.CreateAlertResult{}, err
	}
	v, err := r.Repository.CreateAlert(ctx, a)
	if err == nil {
		err = r.interrupt("create", true)
	}
	return v, err
}

func (r *interruptionRepository) CompareAndSetAlert(ctx context.Context, tenant, id string, version store.VersionToken, a domain.Alert) (store.StoredAlert, error) {
	stage := "update"
	if a.Status.Terminal() {
		stage = "close"
	}
	if err := r.interrupt(stage, false); err != nil {
		return store.StoredAlert{}, err
	}
	v, err := r.Repository.CompareAndSetAlert(ctx, tenant, id, version, a)
	if err == nil {
		err = r.interrupt(stage, true)
	}
	return v, err
}

func (r *interruptionRepository) AppendAlertLogs(ctx context.Context, logs []domain.AlertLog) ([]store.AppendAlertLogItemResult, error) {
	if err := r.interrupt("logs", false); err != nil {
		return nil, err
	}
	v, err := r.Repository.AppendAlertLogs(ctx, logs)
	if err == nil {
		err = r.interrupt("logs", true)
	}
	return v, err
}

func (r *interruptionRepository) CompareAndSetEventResult(ctx context.Context, tenant, id string, version store.VersionToken, result store.EventResult) (store.StoredEvent, error) {
	if err := r.interrupt("result", false); err != nil {
		return store.StoredEvent{}, err
	}
	v, err := r.Repository.CompareAndSetEventResult(ctx, tenant, id, version, result)
	if err == nil {
		err = r.interrupt("result", true)
	}
	return v, err
}

type interruptionCache struct {
	RecentAlertCache
	failed bool
	after  bool
}

func (c *interruptionCache) PutCurrent(ctx context.Context, a store.StoredAlert) error {
	if !c.failed && !c.after {
		c.failed = true
		return errors.New("injected cache failure")
	}
	err := c.RecentAlertCache.PutCurrent(ctx, a)
	if !c.failed && c.after {
		c.failed = true
		return errors.New("cache response lost")
	}
	return err
}

func TestInterruptedStepsWithoutPersistentCheckpoints(t *testing.T) {
	for _, stage := range []string{"create", "update", "close", "logs", "result", "cache"} {
		for _, after := range []bool{false, true} {
			t.Run(stage+map[bool]string{false: "_before_write", true: "_after_write"}[after], func(t *testing.T) {
				base := memory.New()
				shared := newMemoryRecentAlertCache()
				repo := &interruptionRepository{Repository: base}
				p := newTestProcessorWithCache(t, repo, shared, NoopFinalHook{})
				if stage == "update" || stage == "close" {
					persistAndProcess(t, repo, p, testEvent("opening", "warning"))
				}
				event := testEvent("interrupted", "warning")
				event.Content = "updated content"
				if stage == "close" {
					event.Evaluations[0].Action = domain.EventActionResolved
				}
				created, err := base.CreateEvent(t.Context(), event)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				repo.stage, repo.after, repo.cancel = stage, after, cancel
				var cache RecentAlertCache = shared
				if stage == "cache" {
					cache = &interruptionCache{RecentAlertCache: shared, after: after}
				}
				p = newTestProcessorWithCache(t, repo, cache, NoopFinalHook{})
				if _, err = p.ProcessEvent(ctx, created.StoredEvent); err == nil {
					t.Fatal("fault was not observed")
				}
				if stage == "update" {
					active, err := base.FindActiveAlert(t.Context(), store.ActiveAlertKey{BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID, Fingerprint: event.Fingerprint})
					wantContent, wantRevision := "", int64(1)
					if after {
						wantContent, wantRevision = event.Content, 2
					}
					if err != nil || active.Alert.Content != wantContent || active.Alert.Revision != wantRevision {
						t.Fatalf("partial content/revision write: content=%q revision=%d error=%v", active.Alert.Content, active.Alert.Revision, err)
					}
				}
				saved, err := base.GetEvent(t.Context(), event.BKTenantID, event.EventID)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := json.Marshal(saved.Processing)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(raw), `"plan"`) || strings.Contains(string(raw), `"policy_context"`) {
					t.Fatalf("persisted checkpoint: %s", raw)
				}
				// 新处理器不持有上次内存裁决；共享缓存刻意保留旧值，检验 CAS 冲突后的真实状态重读。
				restarted := newTestProcessorWithCache(t, repo, shared, NoopFinalHook{})
				restarted.clock = fixedClock{time.Date(2026, 9, 1, 0, 20, 0, 0, time.UTC)}
				if _, err = restarted.ProcessEvent(t.Context(), saved); err != nil {
					t.Fatalf("replay stuck after %s: %v", stage, err)
				}
				if stage == "update" {
					active, err := base.FindActiveAlert(t.Context(), store.ActiveAlertKey{BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID, Fingerprint: event.Fingerprint})
					if err != nil || active.Alert.Content != event.Content || active.Alert.Revision != 2 {
						t.Fatalf("replay lost content or advanced revision twice: content=%q revision=%d error=%v", active.Alert.Content, active.Alert.Revision, err)
					}
				}
				next := testEvent("next", "critical")
				persistAndProcess(t, repo, restarted, next)
				active, err := base.FindActiveAlert(t.Context(), store.ActiveAlertKey{BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID, Fingerprint: event.Fingerprint})
				if err != nil || active.Alert.Status != domain.AlertStatusActive || active.Alert.Severity != "critical" {
					t.Fatalf("next trigger failed: %+v %v", active.Alert, err)
				}
				resolved := testEvent("next-resolved", "critical")
				resolved.Evaluations[0].Action = domain.EventActionResolved
				persistAndProcess(t, repo, restarted, resolved)
				if _, err = base.FindActiveAlert(t.Context(), store.ActiveAlertKey{BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID, Fingerprint: event.Fingerprint}); !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("next recovery failed: %v", err)
				}
				reopened := testEvent("reopened", "warning")
				persistAndProcess(t, repo, restarted, reopened)
				closed := testEvent("next-closed", "warning")
				closed.Evaluations[0].Action = domain.EventActionClosed
				persistAndProcess(t, repo, restarted, closed)
				if _, err = base.FindActiveAlert(t.Context(), store.ActiveAlertKey{BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID, Fingerprint: event.Fingerprint}); !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("next close failed: %v", err)
				}
			})
		}
	}
}

// 调度器对普通失败记录拒绝并出队；此处复现出队后的真实业务状态，直接处理后续事件。
func TestDroppedPartialEventDoesNotPoisonFollowingEvent(t *testing.T) {
	for _, stage := range []string{"create", "update", "close", "logs", "result", "cache"} {
		t.Run(stage, func(t *testing.T) {
			base := memory.New()
			shared := newMemoryRecentAlertCache()
			repo := &interruptionRepository{Repository: base, cancel: func() {}}
			p := newTestProcessorWithCache(t, repo, shared, NoopFinalHook{})
			if stage == "update" || stage == "close" {
				persistAndProcess(t, repo, p, testEvent("opening", "warning"))
			}
			event := testEvent("dropped", "warning")
			if stage == "close" {
				event.Evaluations[0].Action = domain.EventActionResolved
			}
			created, err := base.CreateEvent(t.Context(), event)
			if err != nil {
				t.Fatal(err)
			}
			repo.stage = stage
			if stage == "cache" {
				p = newTestProcessorWithCache(t, repo, &interruptionCache{RecentAlertCache: shared}, NoopFinalHook{})
			}
			if _, err = p.ProcessEvent(t.Context(), created.StoredEvent); err == nil {
				t.Fatal("ordinary failure was hidden")
			}
			pending, err := base.GetEvent(t.Context(), event.BKTenantID, event.EventID)
			if err != nil {
				t.Fatal(err)
			}
			rejected, err := base.CompareAndSetEventResult(t.Context(), event.BKTenantID, event.EventID, pending.Version, store.EventResult{State: domain.EventProcessStateRejected, Outcome: "processing_failed", ReasonCode: "processing_failed", ProcessedAt: event.ReceivedAt})
			if err != nil {
				t.Fatal(err)
			}
			p = newTestProcessorWithCache(t, repo, shared, NoopFinalHook{})
			if got, err := p.ProcessEvent(t.Context(), rejected); err != nil || got.EventState != domain.EventProcessStateRejected {
				t.Fatal("discarded event was executed again", err)
			}
			persistAndProcess(t, repo, p, testEvent("following", "critical"))
			next := testEvent("following-closed", "critical")
			next.Evaluations[0].Action = domain.EventActionClosed
			persistAndProcess(t, repo, p, next)
			if _, err := base.FindActiveAlert(t.Context(), store.ActiveAlertKey{BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID, Fingerprint: event.Fingerprint}); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("following close failed: %v", err)
			}
		})
	}
}

func TestCreateResponseLostAdoptsStoredAlert(t *testing.T) {
	base := memory.New()
	repo := &interruptionRepository{Repository: base, stage: "create", after: true, cancel: func() {}}
	p := newTestProcessorWithCache(t, repo, newMemoryRecentAlertCache(), NoopFinalHook{})
	event := testEvent("create-response-lost", "warning")
	created, err := base.CreateEvent(t.Context(), event)
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.ProcessEvent(t.Context(), created.StoredEvent)
	if err != nil || len(result.AlertIDs) != 1 || !repo.fired {
		t.Fatalf("already committed create was not adopted: %+v %v", result, err)
	}
	active, err := base.FindActiveAlert(t.Context(), store.ActiveAlertKey{BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID, Fingerprint: event.Fingerprint})
	if err != nil || active.Alert.AlertID != result.AlertIDs[0] || active.Alert.Revision != 1 {
		t.Fatalf("create response loss changed alert identity: %+v %v", active.Alert, err)
	}
}
