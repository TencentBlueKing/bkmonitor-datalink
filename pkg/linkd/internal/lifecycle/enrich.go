// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle

import (
	"context"
	"fmt"

	"linkd/internal/domain"
)

// EnrichInput 是 Enricher 可读取但不得修改的 Event 与待创建 Alert 副本。
type EnrichInput struct {
	Event domain.Event
	Alert domain.Alert
}

// EnrichResult 只允许设置 Alert 的 enrich_status 与 enrich。
type EnrichResult struct {
	Status domain.EnrichStatus
	Data   domain.JSONObject
}

// AlertEnricher 为新建 Alert 提供同步、无副作用且可重试的丰富入口。
type AlertEnricher interface {
	Enrich(ctx context.Context, input EnrichInput) (EnrichResult, error)
}

// NoopEnricher 保留丰富入口，但不产生实际丰富数据。
type NoopEnricher struct{}

// Enrich 把未配置丰富规则视为一次成功的空丰富，使正常创建不会落入 pending。
func (NoopEnricher) Enrich(ctx context.Context, _ EnrichInput) (EnrichResult, error) {
	if err := ctx.Err(); err != nil {
		return EnrichResult{}, err
	}
	return EnrichResult{Status: domain.EnrichStatusSucceeded, Data: domain.JSONObject{}}, nil
}

func (p *Processor) enrichNewAlert(
	ctx context.Context,
	event domain.Event,
	alert domain.Alert,
) (domain.Alert, error) {
	normalized, err := alert.Normalize()
	if err != nil {
		return domain.Alert{}, fmt.Errorf("normalize base alert before enrich: %w", err)
	}
	result, failureReason := p.callEnricher(ctx, EnrichInput{
		Event: event.Clone(),
		Alert: normalized.Clone(),
	})
	if err := ctx.Err(); err != nil {
		return domain.Alert{}, err
	}
	if failureReason == "" {
		var normalizeErr error
		result.Data, normalizeErr = result.Data.Normalize()
		switch {
		case result.Status != domain.EnrichStatusSucceeded &&
			result.Status != domain.EnrichStatusPartial &&
			result.Status != domain.EnrichStatusFailed:
			failureReason = "invalid_enrich_status"
		case normalizeErr != nil:
			failureReason = "invalid_enrich_data"
		}
	}
	if failureReason != "" {
		p.logger.WarnContext(
			ctx,
			"alert enrich degraded",
			"bk_tenant_id", event.BKTenantID,
			"event_id", event.EventID,
			"alert_id", normalized.AlertID,
			"reason_code", failureReason,
		)
		result = EnrichResult{Status: domain.EnrichStatusFailed, Data: domain.JSONObject{}}
	}
	normalized.EnrichStatus = result.Status
	normalized.Enrich = result.Data.Clone()
	normalized, err = normalized.Normalize()
	if err != nil {
		return domain.Alert{}, fmt.Errorf("normalize enriched alert: %w", err)
	}
	return normalized, nil
}

func (p *Processor) callEnricher(
	ctx context.Context,
	input EnrichInput,
) (result EnrichResult, failureReason string) {
	defer func() {
		if recover() != nil {
			result = EnrichResult{}
			failureReason = "enricher_panic"
		}
	}()
	var err error
	result, err = p.enricher.Enrich(ctx, input)
	if err != nil {
		return EnrichResult{}, "enricher_error"
	}
	return result, ""
}
