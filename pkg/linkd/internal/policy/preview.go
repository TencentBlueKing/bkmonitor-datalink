// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"linkd/internal/domain"
)

// PreviewRequest 明确租户、策略版本/候选 Spec 和唯一 Event/Alert 输入；不执行丰富或写入。
type PreviewRequest struct {
	Scope
	ID            string          `json:"id,omitempty"`
	Version       int64           `json:"version,omitempty"`
	Spec          json.RawMessage `json:"spec,omitempty"`
	EventID       string          `json:"event_id,omitempty"`
	AlertID       string          `json:"alert_id,omitempty"`
	Event         *domain.Event   `json:"event,omitempty"`
	Alert         *domain.Alert   `json:"alert,omitempty"`
	Severity      string          `json:"severity,omitempty"`
	At            *time.Time      `json:"at,omitempty"`
	Rely          bool            `json:"rely,omitempty"`
	OriginAlertID string          `json:"origin_alert_id,omitempty"`
}

// PreviewVerdict 返回每个 evaluation 的结果；输入 Alert 时仅一项。
type PreviewVerdict struct {
	Severity string `json:"severity"`
	Verdict
}

// PreviewResponse 仅描述匹配和准入条件，不把当前匹配伪装成已完成计数/屏蔽/合并。
type PreviewResponse struct {
	ID          string           `json:"id"`
	Version     int64            `json:"version"`
	Compiled    Summary          `json:"compiled"`
	Mode        string           `json:"mode"`
	At          time.Time        `json:"at"`
	Evaluations []PreviewVerdict `json:"evaluations"`
}

// PreviewFacts 提供租户隔离的只读事实查询，不暴露写端口。
type PreviewFacts interface {
	Event(context.Context, string, string) (domain.Event, error)
	Alert(context.Context, string, string) (domain.Alert, error)
}

// Previewer 与 Lifecycle 复用 Evaluate；最多四个同时调试请求，避免占满正式工作池。
type Previewer struct {
	policies  *Service
	facts     PreviewFacts
	targets   TargetReader
	relations RelationLookup
	level     func() LevelMapper
	slots     chan struct{}
}

// NewPreviewer 装配真实已保存事实和只读资源；level 每次请求冻结一次。
func NewPreviewer(policies *Service, facts PreviewFacts, targets TargetReader, relations RelationLookup, level func() LevelMapper) *Previewer {
	return &Previewer{policies: policies, facts: facts, targets: targets, relations: relations, level: level, slots: make(chan struct{}, 4)}
}

// ErrPreviewCapacity 表示调试并发已满，不等待无界队列。
var ErrPreviewCapacity = errors.New("policy preview capacity exceeded")

// Preview 不占窗口、不计数、不建关系；At 只用于时段判定，不改变 Event 时间。
func (p *Previewer) Preview(ctx context.Context, request PreviewRequest) (PreviewResponse, error) {
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	default:
		return PreviewResponse{}, ErrPreviewCapacity
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := request.Validate(); err != nil {
		return PreviewResponse{}, err
	}
	inputs := 0
	for _, present := range []bool{request.EventID != "", request.AlertID != "", request.Event != nil, request.Alert != nil} {
		if present {
			inputs++
		}
	}
	if inputs != 1 {
		return PreviewResponse{}, fmt.Errorf("%w: choose one Event/Alert input", ErrInvalid)
	}
	release, compiled, err := ResolvePreviewPolicy(ctx, p.policies, request.Scope, request.ID, request.Version, request.Spec)
	if err != nil {
		return PreviewResponse{}, err
	}
	level := LevelMapper(DefaultKACLevel)
	if p.level != nil {
		level = p.level()
	}
	relation := RelationContext{Lookup: p.relations}
	if request.OriginAlertID != "" {
		if !request.Rely || p.facts == nil {
			return PreviewResponse{}, ErrInvalid
		}
		origin, err := p.facts.Alert(ctx, request.TenantID, request.OriginAlertID)
		if err != nil {
			return PreviewResponse{}, err
		}
		if origin.BKTenantID != request.TenantID || origin.AlertID != request.OriginAlertID {
			return PreviewResponse{}, ErrInvalid
		}
		view, err := AlertView(origin, compiled.Common.FieldMappings, level, RelationContext{})
		if err != nil {
			return PreviewResponse{}, err
		}
		ref, found, err := view.CanonicalInstance(ctx)
		if err != nil {
			return PreviewResponse{}, err
		}
		if !found {
			return PreviewResponse{}, ErrUnavailable
		}
		relation.Origin = ref
	}
	at := time.Now().UTC()
	if request.At != nil {
		at = *request.At
		if at.IsZero() {
			return PreviewResponse{}, ErrInvalid
		}
	}
	response := PreviewResponse{ID: release.ID, Version: release.Version, Compiled: compiled.Summary, Mode: "matching_only", At: at, Evaluations: []PreviewVerdict{}}
	evaluate := func(severity string, view *FactView) error {
		verdict, err := Evaluate(ctx, release, compiled, view, p.targets, at, request.Rely)
		if err == nil {
			response.Evaluations = append(response.Evaluations, PreviewVerdict{Severity: severity, Verdict: verdict})
		}
		return err
	}
	if request.EventID != "" {
		if p.facts == nil {
			return PreviewResponse{}, ErrUnavailable
		}
		event, err := p.facts.Event(ctx, request.TenantID, request.EventID)
		if err != nil {
			return PreviewResponse{}, err
		}
		if event.EventID != request.EventID {
			return PreviewResponse{}, ErrInvalid
		}
		request.Event = &event
	}
	if request.AlertID != "" {
		if p.facts == nil {
			return PreviewResponse{}, ErrUnavailable
		}
		alert, err := p.facts.Alert(ctx, request.TenantID, request.AlertID)
		if err != nil {
			return PreviewResponse{}, err
		}
		if alert.AlertID != request.AlertID {
			return PreviewResponse{}, ErrInvalid
		}
		request.Alert = &alert
	}
	if request.Event != nil {
		event := request.Event
		if event.BKTenantID != request.TenantID {
			return PreviewResponse{}, ErrInvalid
		}
		if err := event.Validate(); err != nil {
			return PreviewResponse{}, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		for _, evaluation := range event.Evaluations {
			if request.Severity != "" && request.Severity != evaluation.Severity {
				continue
			}
			view, err := EventView(*event, evaluation.Severity, compiled.Common.FieldMappings, level, relation)
			if err != nil {
				return PreviewResponse{}, err
			}
			if err := evaluate(evaluation.Severity, view); err != nil {
				return PreviewResponse{}, err
			}
		}
	} else {
		alert := request.Alert
		if alert.BKTenantID != request.TenantID {
			return PreviewResponse{}, ErrInvalid
		}
		if err := alert.Validate(); err != nil {
			return PreviewResponse{}, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		if request.Severity != "" && request.Severity != alert.Severity {
			return PreviewResponse{}, ErrInvalid
		}
		view, err := AlertView(*alert, compiled.Common.FieldMappings, level, relation)
		if err != nil {
			return PreviewResponse{}, err
		}
		if err := evaluate(alert.Severity, view); err != nil {
			return PreviewResponse{}, err
		}
	}
	if len(response.Evaluations) == 0 {
		return PreviewResponse{}, fmt.Errorf("%w: requested severity missing", ErrInvalid)
	}
	return response, nil
}

// ResolvePreviewPolicy 为匹配预览和隔离模拟统一选择精确发布版本或候选配置。
func ResolvePreviewPolicy(ctx context.Context, service *Service, scope Scope, id string, version int64, spec json.RawMessage) (Release, *Compiled, error) {
	if err := scope.Validate(); err != nil {
		return Release{}, nil, err
	}
	if len(spec) > 0 {
		if id != "" || version != 0 {
			return Release{}, nil, ErrInvalid
		}
		compiled, err := Compile(scope.Kind, spec)
		if err != nil {
			return Release{}, nil, errors.Join(ErrInvalid, err)
		}
		return Release{Scope: scope, ID: "preview", Spec: compiled.Canonical, Compiled: compiled.Summary}, compiled, nil
	}
	if service == nil {
		return Release{}, nil, ErrUnavailable
	}
	release, err := service.GetRelease(ctx, scope, id, version)
	if err != nil {
		return Release{}, nil, err
	}
	compiled, err := Compile(release.Kind, release.Spec)
	return release, compiled, err
}
