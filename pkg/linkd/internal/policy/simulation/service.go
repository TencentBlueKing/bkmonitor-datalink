// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package simulation 在请求私有内存中运行正式 Lifecycle 和策略裁决，使用显式虚拟时间。
// 不执行丰富、外部输出或生产状态写入；成功窗口只建立虚拟成员标记，不创建 KAC 文档或处置。
package simulation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	"linkd/internal/policy"
	policyruntime "linkd/internal/policy/runtime"
	"linkd/internal/runtimeconfig"
	"linkd/internal/store"
	"linkd/internal/store/memory"
)

// Step 在 At 推进虚拟时间；Event/EventID 二选一，均省略时只触发窗口检查。
type Step struct {
	At      time.Time     `json:"at"`
	Event   *domain.Event `json:"event,omitempty"`
	EventID string        `json:"event_id,omitempty"`
}

// Request 选择单个不可变策略或候选配置；不加载生产窗口或活动 Alert。
type Request struct {
	policy.Scope
	ID      string          `json:"id,omitempty"`
	Version int64           `json:"version,omitempty"`
	Spec    json.RawMessage `json:"spec,omitempty"`
	Steps   []Step          `json:"steps"`
}

// Result 保存该步完成后的诊断；Alert 身份和状态只属于本次模拟。
type Result struct {
	Replayed bool                  `json:"replayed"`
	At       time.Time             `json:"at"`
	EventID  string                `json:"event_id,omitempty"`
	Outcome  string                `json:"outcome"`
	Decision *store.PolicyDecision `json:"decision,omitempty"`
	Alerts   []domain.Alert        `json:"alerts"`
	Windows  []WindowResult        `json:"windows"`
}

// WindowResult 表示虚拟控制面裁决，不表示已创建父告警或已执行处置。
type WindowResult struct {
	ID        string    `json:"id"`
	StartedAt time.Time `json:"started_at"`
	Deadline  time.Time `json:"deadline"`
	Outcome   string    `json:"outcome"`
	Members   []string  `json:"members"`
}

// Response 是单次有界模拟的完整轨迹，不保存为业务事实。
type Response struct {
	policy.Scope
	ID       string         `json:"id"`
	Version  int64          `json:"version"`
	Compiled policy.Summary `json:"compiled"`
	Mode     string         `json:"mode"`
	Steps    []Result       `json:"steps"`
}

// Service 的资源端口只有读取能力；并发上限为二，整次模拟最多十秒、128步和30天。
type Service struct {
	policies *policy.Service
	facts    policy.PreviewFacts
	targets  policy.TargetReader
	severity func() runtimeconfig.Snapshot
	upgrade  string
	slots    chan struct{}
}

// New 注入只读事实、实时资源和严重等级配置；模拟状态由每次请求独占。
func New(policies *policy.Service, facts policy.PreviewFacts, targets policy.TargetReader, severity func() runtimeconfig.Snapshot, upgrade string) *Service {
	return &Service{policies: policies, facts: facts, targets: targets, severity: severity, upgrade: upgrade, slots: make(chan struct{}, 2)}
}

// Run 从空状态重放事件，重复 Event 复用首次结果；所有时间推进先检查到期窗口，再消费该时刻事件。
// 资源查询反映请求时环境，不宣称历史资源复原或真实调度时延。失败请求不返回半份成功轨迹。
func (s *Service) Run(ctx context.Context, request Request) (Response, error) {
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return Response{}, policy.ErrPreviewCapacity
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := request.Validate(); err != nil {
		return Response{}, err
	}
	if request.Kind != policy.Suppression && request.Kind != policy.Merge {
		return Response{}, policy.ErrInvalid
	}
	if len(request.Steps) < 1 || len(request.Steps) > 128 {
		return Response{}, policy.ErrInvalid
	}
	raw, err := json.Marshal(request)
	if err != nil || len(raw) > 3<<20 {
		return Response{}, policy.ErrInvalid
	}
	var previous time.Time
	for _, step := range request.Steps {
		if step.At.IsZero() || step.At.UnixMilli() < 0 || step.At.UnixMilli() > 1<<46 || step.At.Before(previous) || step.At.Sub(request.Steps[0].At) > 30*24*time.Hour || (step.Event != nil && step.EventID != "") {
			return Response{}, policy.ErrInvalid
		}
		previous = step.At
	}
	release, compiled, err := policy.ResolvePreviewPolicy(ctx, s.policies, request.Scope, request.ID, request.Version, request.Spec)
	if err != nil {
		return Response{}, err
	}
	response := Response{Scope: request.Scope, ID: release.ID, Version: release.Version, Compiled: compiled.Summary, Mode: "state_simulation", Steps: []Result{}}
	// 候选配置仍需满足正式运行时的正版本校验；仅在私有内存使用版本1，响应保留候选版本0。
	if release.Version == 0 {
		release.Version = 1
	}
	severity := runtimeconfig.NewSeverity(config.DefaultSeverityConfig()).SeveritySnapshot()
	if s.severity != nil {
		severity = s.severity()
	}
	repo := memory.New()
	clock := &virtualClock{at: request.Steps[0].At}
	state := newState()
	loader := &policyruntime.Suppressor{Releases: fixedRelease{release}, Targets: s.targets, State: state, Aggregation: state, CurrentAlert: repo.GetAlert, NewAlertID: lifecycle.DeterministicAlertIDGenerator{}.Generate}
	merger := &policyruntime.Merger{Loader: loader, State: state}
	processor, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, noEnrich{}, nil, severity, clock, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithSeverityUpgradePolicy(s.upgrade), lifecycle.WithPolicySnapshotter(fixedRelease{release}), lifecycle.WithNewAlertSuppressor(loader), lifecycle.WithMergeEvaluator(merger))
	if err != nil {
		return Response{}, err
	}
	judge := &policyruntime.MergeJudge{Loader: loader, Windows: state, CurrentAlert: repo.GetAlert}
	ids := map[string]bool{}
	for _, step := range request.Steps {
		if err := ctx.Err(); err != nil {
			return Response{}, err
		}
		clock.at = step.At
		state.now = step.At
		result := Result{At: step.At, Outcome: "time_advanced", Alerts: []domain.Alert{}, Windows: []WindowResult{}}
		if err := judgeWindows(ctx, request.TenantID, state, judge, processor, repo, clock, ids, severity.KACLevel, &result); err != nil {
			return Response{}, err
		}
		event := step.Event
		if step.EventID != "" {
			if s.facts == nil {
				return Response{}, policy.ErrUnavailable
			}
			found, err := s.facts.Event(ctx, request.TenantID, step.EventID)
			if err != nil {
				return Response{}, err
			}
			if found.EventID != step.EventID {
				return Response{}, policy.ErrAccess
			}
			event = &found
		}
		if event != nil {
			value, err := event.Normalize()
			if err != nil {
				return Response{}, policy.ErrInvalid
			}
			if value.BKTenantID != request.TenantID {
				return Response{}, policy.ErrAccess
			}
			if value.EnrichStatus == domain.EnrichStatusPending || value.MergeOrigin != nil || value.EventSourceID == domain.BuiltinMergeEventSourceID {
				return Response{}, policy.ErrInvalid
			}
			value.RelatedAlertIDs = nil
			if err := value.Validate(); err != nil {
				return Response{}, fmt.Errorf("%w: event", policy.ErrInvalid)
			}
			snapshot := value.EventEnrichment.Clone()
			value.EventEnrichment = domain.EventEnrichment{EnrichStatus: domain.EnrichStatusPending}
			created, err := repo.CreateEvent(ctx, value)
			if err != nil {
				return Response{}, errors.Join(policy.ErrInvalid, err)
			}
			stored := created.StoredEvent
			if stored.Event.EnrichStatus == domain.EnrichStatusPending {
				stored, err = repo.CompareAndSetEventEnrichment(ctx, value.BKTenantID, value.EventID, stored.Version, snapshot)
				if err != nil {
					return Response{}, err
				}
			} else if !reflect.DeepEqual(stored.Event.EventEnrichment, snapshot) {
				return Response{}, policy.ErrInvalid
			}
			processed, err := processor.ProcessEvent(ctx, stored)
			if err != nil {
				return Response{}, err
			}
			saved, err := repo.GetEvent(ctx, request.TenantID, value.EventID)
			if err != nil {
				return Response{}, err
			}
			result.Replayed = !created.Created
			result.EventID = value.EventID
			result.Outcome = string(processed.Outcome)
			result.Decision = saved.Processing.PolicyDecision
			for _, id := range saved.Event.RelatedAlertIDs {
				ids[id] = true
			}
			if err := judgeWindows(ctx, request.TenantID, state, judge, processor, repo, clock, ids, severity.KACLevel, &result); err != nil {
				return Response{}, err
			}
		}
		sorted := make([]string, 0, len(ids))
		for id := range ids {
			sorted = append(sorted, id)
		}
		slices.Sort(sorted)
		for _, id := range sorted {
			a, err := repo.GetAlert(ctx, request.TenantID, id)
			if err != nil {
				return Response{}, err
			}
			result.Alerts = append(result.Alerts, a.Alert)
		}
		response.Steps = append(response.Steps, result)
		// 响应包含逐步快照，限制总量以免128个大Event放大成无界轨迹。
		encoded, err := json.Marshal(response)
		if err != nil || len(encoded) > 8<<20 {
			return Response{}, policy.ErrInvalid
		}
	}
	return response, nil
}

type virtualClock struct{ at time.Time }

func (c *virtualClock) Now() time.Time { return c.at }

type noEnrich struct{}

func (noEnrich) Enrich(context.Context, enrich.Input) (enrich.Result, error) {
	return enrich.Result{}, policy.ErrUnavailable
}

type fixedRelease struct{ release policy.Release }

func (f fixedRelease) GetRelease(_ context.Context, scope policy.Scope, id string, version int64) (policy.Release, error) {
	if scope != f.release.Scope || id != f.release.ID || version != f.release.Version {
		return policy.Release{}, policy.ErrAccess
	}
	return f.release, nil
}

func (f fixedRelease) Snapshot(_ context.Context, event domain.Event, at time.Time) (*store.PolicyContext, error) {
	if event.BKTenantID != f.release.TenantID {
		return nil, policy.ErrAccess
	}
	return &store.PolicyContext{EvaluatedAt: at, Releases: []store.PolicyReleaseRef{{Kind: string(f.release.Kind), ID: f.release.ID, Version: f.release.Version, Digest: f.release.Compiled.Digest}}}, nil
}

func judgeWindows(ctx context.Context, tenant string, state *state, judge *policyruntime.MergeJudge, processor *lifecycle.Processor, repo *memory.Repository, clock *virtualClock, ids map[string]bool, level policy.LevelMapper, result *Result) error {
	keys := make([]string, 0, len(state.windows))
	for id := range state.windows {
		keys = append(keys, id)
	}
	slices.Sort(keys)
	for _, id := range keys {
		w := state.windows[id]
		if w.Frozen != nil {
			continue
		}
		judgment, err := judge.Evaluate(ctx, tenant, id, clock.at, level)
		if err != nil {
			return err
		}
		if !judgment.Frozen {
			continue
		}
		frozen := judgment.Window.Frozen
		result.Windows = append(result.Windows, WindowResult{ID: id, StartedAt: time.UnixMilli(w.StartedAtMillis), Deadline: time.UnixMilli(w.DeadlineMillis), Outcome: frozen.Outcome, Members: append([]string{}, frozen.MemberIDs...)})
		selected := map[string]bool{}
		for _, member := range frozen.MemberIDs {
			selected[member] = true
		}
		for _, member := range w.Members {
			current, err := repo.GetAlert(ctx, tenant, member.Main.AlertID)
			if err != nil {
				return err
			}
			ids[current.Alert.AlertID] = true
			if current.Alert.Status.Terminal() {
				continue
			}
			if frozen.Outcome == "succeeded" && selected[current.Alert.AlertID] {
				// 成功裁决只产生模拟关系标记，使后续事件不会重复入窗；不伪造实际父告警或处置结果。
				next := current.Alert.Clone()
				next.UpdateAt = clock.at
				if !next.UpdateAt.After(current.Alert.UpdateAt) {
					next.UpdateAt = current.Alert.UpdateAt.Add(time.Nanosecond)
				}
				next.Merge, _, err = next.Merge.LinkRelation(id, frozen.OperationID)
				if err != nil {
					return err
				}
				next, err = domain.PrepareAlertReplacement(current.Alert, next)
				if err != nil {
					return err
				}
				if _, err = repo.CompareAndSetAlert(ctx, tenant, next.AlertID, current.Version, next); err != nil {
					return err
				}
			} else {
				if _, err := processor.ReleaseMergeWindow(ctx, tenant, current.Alert.AlertID, id); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
