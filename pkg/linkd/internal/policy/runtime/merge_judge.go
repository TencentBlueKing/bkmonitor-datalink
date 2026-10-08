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
	"slices"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	"linkd/internal/store"
)

// MergeWindows 是独立裁决消费的完整快照与原子冻结端口，不在 Event 主流程等待窗口结束。
type MergeWindows interface {
	ReadMergeWindow(context.Context, string, string) (redisstate.MergeWindow, bool, error)
	FreezeMergeWindow(context.Context, string, string, int64, time.Time, []redisstate.MergeCandidate) (redisstate.MergeWindow, bool, error)
}

// MergeJudgment 提供持久化业务裁决所需的当前成员快照；Frozen 才能进入父 Event 创建步骤。
// 缓存丢失不伪造窗口；调用者按 Alert 已保存的截止时间结束等待。
type MergeJudgment struct {
	Window  redisstate.MergeWindow
	Members []store.StoredAlert
	Lost    bool
	Frozen  bool
}

// MergeJudge 重读真实成员及冻结策略，不把 Redis 的 committed 当作当前仍有效的证明。
type MergeJudge struct {
	Loader       *Suppressor
	Windows      MergeWindows
	CurrentAlert func(context.Context, string, string) (store.StoredAlert, error)
}

// Evaluate 冻结条件满足的成员集合；部分读取、租户错误及取消均不产生半个成功结果。
// 返回快照必须先写入持久化业务裁决，再创建内部 Event；不能在调用方重试时重新渲染已保存的裁决。
func (j *MergeJudge) Evaluate(ctx context.Context, tenant, id string, at time.Time, level func(string) (string, error)) (MergeJudgment, error) {
	if j.Loader == nil || j.Windows == nil || j.CurrentAlert == nil || at.IsZero() {
		return MergeJudgment{}, policy.ErrInvalid
	}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	w, found, err := j.Windows.ReadMergeWindow(call, tenant, id)
	if err != nil {
		return MergeJudgment{}, err
	}
	if !found {
		return MergeJudgment{Lost: true}, nil
	}
	if w.TenantID != tenant || w.ID != id {
		return MergeJudgment{}, policy.ErrAccess
	}
	if w.Frozen != nil {
		return j.frozenSnapshots(call, w)
	}
	if at.UnixMilli() >= w.DeadlineMillis && j.Loader.Observations != nil && j.Loader.Observations.metrics != nil {
		j.Loader.Observations.metrics.ObservePolicyDelay(ctx, "merge", at.Sub(time.UnixMilli(w.DeadlineMillis)))
	}
	if w.Cyclic && at.UnixMilli() < w.DeadlineMillis {
		return MergeJudgment{Window: w}, nil
	}
	f, err := j.Loader.load(call, tenant, store.PolicyReleaseRef{Kind: "merge", ID: w.Policy.ID, Version: w.Policy.Version, Digest: w.Policy.Digest})
	if err != nil {
		return MergeJudgment{}, err
	}
	if f.Compiled.Merge == nil || len(f.Compiled.Conditions) != w.GroupCount || f.Compiled.Merge.Cyclic != w.Cyclic || f.Compiled.Merge.Cycle*1000 != w.DeadlineMillis-w.StartedAtMillis {
		return MergeJudgment{}, policy.ErrInvalid
	}
	targets := &evaluationTargets{reader: j.Loader.Targets}
	selected := []redisstate.MergeCandidate{}
	snapshots := []store.StoredAlert{}
	snapshotBytes := 0
	for _, member := range w.Members {
		if !member.Committed {
			continue
		}
		current, err := j.CurrentAlert(call, tenant, member.Main.AlertID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return MergeJudgment{}, err
		}
		a := current.Alert
		if a.BKTenantID != tenant || a.AlertID != member.Main.AlertID || a.EventSourceID != member.Main.EventSourceID || a.Fingerprint != member.Main.Fingerprint {
			return MergeJudgment{}, policy.ErrAccess
		}
		if a.Status != domain.AlertStatusActive || a.Shield.Active || a.Merge == nil || a.Merge.Role != "original" {
			continue
		}
		waiting := false
		for _, wait := range a.Merge.Pending {
			if wait.WindowID == id {
				if wait.Policy != w.Policy || wait.GroupKey != w.GroupKey || wait.StartedAt.UnixMilli() != w.StartedAtMillis || wait.Deadline.UnixMilli() != w.DeadlineMillis {
					return MergeJudgment{}, policy.ErrInvalid
				}
				waiting = true
			}
		}
		if !waiting {
			continue
		}
		view, err := policy.AlertView(a, f.Compiled.Common.FieldMappings, level, policy.RelationContext{})
		if err != nil {
			return MergeJudgment{}, err
		}
		verdict, err := j.Loader.evaluate(call, f.Release, f.Compiled, view, targets, time.UnixMilli(w.StartedAtMillis), false, true)
		if err != nil {
			return MergeJudgment{}, err
		}
		if !verdict.Evaluated {
			return MergeJudgment{}, policy.ErrUnavailable
		}
		if !verdict.Matched || verdict.GroupKey != w.GroupKey {
			continue
		}
		groups := []int{}
		for _, g := range verdict.Groups {
			if g.Matched {
				groups = append(groups, g.Index)
			}
		}
		selected = append(selected, redisstate.MergeCandidate{AlertID: a.AlertID, Groups: groups})
		raw, err := json.Marshal(current.Alert)
		if err != nil {
			return MergeJudgment{}, err
		}
		snapshotBytes += len(raw)
		if snapshotBytes > 32<<20 {
			return MergeJudgment{}, policy.ErrUnavailable
		}
		snapshots = append(snapshots, current)
	}
	frozen, ok, err := j.Windows.FreezeMergeWindow(call, tenant, id, w.Revision, at, selected)
	if err != nil {
		return MergeJudgment{}, err
	}
	if !ok {
		return MergeJudgment{Window: w}, nil
	}
	ids := make([]string, 0, len(snapshots))
	for _, s := range snapshots {
		ids = append(ids, s.Alert.AlertID)
	}
	if !slices.Equal(ids, frozen.Frozen.MemberIDs) {
		return j.frozenSnapshots(call, frozen)
	}
	return MergeJudgment{Window: frozen, Members: snapshots, Frozen: true}, nil
}

// 已冻结集合不会因另一轮读取变化重新求条件。持久化裁决已存在时，调用者必须优先复用其快照。
func (j *MergeJudge) frozenSnapshots(ctx context.Context, w redisstate.MergeWindow) (MergeJudgment, error) {
	result := MergeJudgment{Window: w, Frozen: true}
	snapshotBytes := 0
	for _, id := range w.Frozen.MemberIDs {
		a, err := j.CurrentAlert(ctx, w.TenantID, id)
		if err != nil {
			return MergeJudgment{}, err
		}
		if a.Alert.BKTenantID != w.TenantID || a.Alert.AlertID != id {
			return MergeJudgment{}, policy.ErrAccess
		}
		raw, err := json.Marshal(a.Alert)
		if err != nil {
			return MergeJudgment{}, err
		}
		snapshotBytes += len(raw)
		if snapshotBytes > 32<<20 {
			return MergeJudgment{}, policy.ErrUnavailable
		}
		result.Members = append(result.Members, a)
	}
	return result, nil
}
