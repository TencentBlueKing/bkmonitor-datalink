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
	"errors"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/policy"
	"linkd/internal/processingerror"
	"linkd/internal/store"
)

// Snapshotter 在一次内存求值中固定候选版本；明确的控制面传输故障必须等待恢复，不能发布空策略快照。
type Snapshotter struct {
	Catalog *policy.Catalog
	Level   policy.LevelMapper
}

// WithLevelMapper 复制本次求值的映射，不修改并发 Event 共享的 Snapshotter。
func (s Snapshotter) WithLevelMapper(level func(string) (string, error)) lifecycle.PolicySnapshotter {
	s.Level = level
	return s
}

// Snapshot 在 Redis 副作用前固定本次处理时间，不用来源时间混入防抖窗口。
func (s Snapshotter) Snapshot(ctx context.Context, event domain.Event, at time.Time) (*store.PolicyContext, error) {
	snapshot := &store.PolicyContext{EvaluatedAt: at, Releases: []store.PolicyReleaseRef{}}
	if s.Catalog == nil {
		snapshot.ReasonCode = "policy_load_failed"
		return snapshot, nil
	}
	policies, err := s.Catalog.Candidates(ctx, event.BKTenantID, eventCandidateReader(event, s.Level))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if errors.Is(err, processingerror.ErrControlPlaneUnavailable) || errors.Is(err, policy.ErrAccess) || errors.Is(err, policy.ErrInvalid) {
		return nil, err
	}
	if err != nil {
		snapshot.ReasonCode = "policy_load_failed"
		return snapshot, nil
	}
	for _, item := range policies {
		if item.Release.TenantID != event.BKTenantID {
			return nil, policy.ErrAccess
		}
		snapshot.Releases = append(snapshot.Releases, store.PolicyReleaseRef{Kind: string(item.Release.Kind), ID: item.Release.ID, Version: item.Release.Version, Digest: item.Compiled.Summary.Digest})
	}
	return snapshot, snapshot.Validate()
}

// eventCandidateReader 联合全部等级的有效视图，不让高等级字段排除低等级可命中的策略。
// 同一字段映射只读一次；任一等级不可求值时保守保留相应字段的全部 posting。
func eventCandidateReader(event domain.Event, level policy.LevelMapper) policy.CandidateReader {
	type viewKey struct{ severity, field, path, readPath string }
	views := make(map[viewKey]*policy.FactView)
	return func(ctx context.Context, field string, mapping policy.FieldMapping) (policy.Value, error) {
		if len(event.Evaluations) == 0 {
			return policy.Value{}, policy.ErrUnavailable
		}
		values := []any{}
		for _, evaluation := range event.Evaluations {
			key := viewKey{severity: evaluation.Severity}
			var mappings map[string]policy.FieldMapping
			if mapping.Path != "" {
				key.field = field
				key.path = mapping.Path
				key.readPath = mapping.ReadPath
				mappings = map[string]policy.FieldMapping{field: mapping}
			}
			view := views[key]
			if view == nil {
				var err error
				view, err = policy.EventView(event, evaluation.Severity, mappings, level, policy.RelationContext{})
				if err != nil {
					return policy.Value{}, err
				}
				views[key] = view
			}
			value, err := view.Field(ctx, field)
			if err != nil {
				return policy.Value{}, err
			}
			if value.Present && value.Data != nil {
				values = append(values, value.Data)
			}
		}
		return policy.Value{Present: len(values) > 0, Data: values}, nil
	}
}
