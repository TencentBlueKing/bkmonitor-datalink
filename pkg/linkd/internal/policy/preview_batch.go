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

// BatchPreviewRequest 只读取调用方已授权的 Alert 候选，不扫描告警或产生策略副作用。
type BatchPreviewRequest struct {
	Scope
	Spec     json.RawMessage `json:"spec"`
	At       *time.Time      `json:"at,omitempty"`
	AlertIDs []string        `json:"alert_ids"`
	Fields   []string        `json:"fields,omitempty"`
}

// BatchPreviewItem 区分普通未命中、不可求值及仅展示字段不可读，不返回底层错误载荷。
type BatchPreviewItem struct {
	AlertID           string         `json:"alert_id"`
	Severity          string         `json:"severity"`
	Evaluated         bool           `json:"evaluated"`
	Matched           bool           `json:"matched"`
	Reason            string         `json:"reason"`
	Fields            map[string]any `json:"fields"`
	UnavailableFields []string       `json:"unavailable_fields,omitempty"`
}

// BatchPreviewResponse 与单条预览一样仅表示条件匹配，不代表建立了屏蔽关系。
type BatchPreviewResponse struct {
	Mode  string             `json:"mode"`
	At    time.Time          `json:"at"`
	Items []BatchPreviewItem `json:"items"`
}

// PreviewBatch 在同一配置、等级映射及评估时间下复用正式 Evaluate，且与单条预览共用并发预算。
func (p *Previewer) PreviewBatch(ctx context.Context, request BatchPreviewRequest) (BatchPreviewResponse, error) {
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	default:
		return BatchPreviewResponse{}, ErrPreviewCapacity
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := request.Validate(); err != nil {
		return BatchPreviewResponse{}, err
	}
	if request.Kind != Shield || len(request.Spec) == 0 || len(request.AlertIDs) == 0 || len(request.AlertIDs) > 32 || len(request.Fields) > 64 {
		return BatchPreviewResponse{}, fmt.Errorf("%w: time shield spec and 1..32 alert_ids required", ErrInvalid)
	}
	for _, id := range request.AlertIDs {
		// AlertID 是完整实体身份，不能使用租户/来源片段的字符限制。
		// 与其他按 ID 查询入口一致，仅约束请求长度，归属由下方事实读取确认。
		if id == "" || len(id) > domain.EntityIDMaxBytes {
			return BatchPreviewResponse{}, fmt.Errorf("%w: invalid alert_id", ErrInvalid)
		}
	}
	if p.facts == nil {
		return BatchPreviewResponse{}, ErrUnavailable
	}
	// 1. 整批只编译一次草稿；字段目录使用与本次正式匹配相同的冻结映射。
	release, compiled, err := ResolvePreviewPolicy(ctx, p.policies, request.Scope, "", 0, request.Spec)
	if err != nil {
		return BatchPreviewResponse{}, err
	}
	if compiled.Shield.ShieldType != "time_shield" {
		return BatchPreviewResponse{}, fmt.Errorf("%w: batch preview requires time_shield", ErrInvalid)
	}
	for _, field := range request.Fields {
		if KACFields()[field] == "" && compiled.Common.FieldMappings[field].Path == "" {
			return BatchPreviewResponse{}, fmt.Errorf("%w: unknown display field %q", ErrInvalid, field)
		}
	}
	at := time.Now().UTC()
	if request.At != nil {
		at = request.At.UTC()
		if at.IsZero() {
			return BatchPreviewResponse{}, ErrInvalid
		}
	}
	level := LevelMapper(DefaultKACLevel)
	if p.level != nil {
		level = p.level()
	}
	response := BatchPreviewResponse{Mode: "matching_only", At: at, Items: make([]BatchPreviewItem, 0, len(request.AlertIDs))}
	// 2. 缺失或不可读候选逐项解释，租户/身份错误终止请求，绝不作为普通未命中。
	for _, id := range request.AlertIDs {
		if err := ctx.Err(); err != nil {
			return BatchPreviewResponse{}, err
		}
		item := BatchPreviewItem{AlertID: id, Fields: map[string]any{}}
		alert, err := p.facts.Alert(ctx, request.TenantID, id)
		if err != nil {
			if errors.Is(err, ErrAccess) || errors.Is(err, ErrInvalid) {
				return BatchPreviewResponse{}, err
			}
			item.Reason = "alert_unavailable"
			if errors.Is(err, ErrNotFound) {
				item.Reason = "alert_not_found"
			}
		} else {
			if alert.BKTenantID != request.TenantID || alert.AlertID != id {
				return BatchPreviewResponse{}, ErrAccess
			}
			item.Severity = alert.Severity
			view, viewErr := AlertView(alert, compiled.Common.FieldMappings, level, RelationContext{Lookup: p.relations})
			if viewErr != nil {
				item.Reason = "alert_view_unavailable"
			} else {
				verdict, evaluateErr := Evaluate(ctx, release, compiled, view, p.targets, at, false)
				if errors.Is(evaluateErr, ErrAccess) || errors.Is(evaluateErr, ErrInvalid) {
					return BatchPreviewResponse{}, evaluateErr
				}
				if evaluateErr != nil {
					item.Reason = "evaluation_unavailable"
				} else {
					item.Evaluated, item.Matched, item.Reason = verdict.Evaluated, verdict.Matched, verdict.Reason
					// 3. 展示读取失败不改变已求出的匹配结论，单独标记缺少的展示证据。
					if item.Matched {
						for _, field := range request.Fields {
							value, err := view.Field(ctx, field)
							if err != nil {
								item.UnavailableFields = append(item.UnavailableFields, field)
							} else if value.Present {
								item.Fields[field] = value.Data
							}
						}
					}
				}
			}
		}
		response.Items = append(response.Items, item)
	}
	if err := ctx.Err(); err != nil {
		return BatchPreviewResponse{}, err
	}
	return response, nil
}
