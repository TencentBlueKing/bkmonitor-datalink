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
	"fmt"
	"time"

	"linkd/internal/domain"
	"linkd/internal/enrich"
)

// AlertEnricher 为新建 Alert 提供同步、无副作用且可重试的丰富入口。
type AlertEnricher interface {
	Enrich(ctx context.Context, input enrich.Input) (enrich.Result, error)
}

func (p *Processor) enrichNewAlert(
	ctx context.Context,
	alert domain.Alert,
) (domain.Alert, error) {
	normalized, err := alert.Normalize()
	if err != nil {
		return domain.Alert{}, fmt.Errorf("normalize base alert before enrich: %w", err)
	}
	startedAt := time.Now()
	chainKind := enrich.ChainUnknown
	if classifier, ok := p.enricher.(EnrichRouteClassifier); ok {
		chainKind = classifier.EnrichChainKind(normalized.EventSourceID)
	}
	p.enrichObserver.Started(ctx, normalized.EventSourceID)
	observation := EnrichObservation{
		EventSourceID: normalized.EventSourceID, Status: domain.EnrichStatusFailed,
		Outcome: EnrichOutcomeError, ChainKind: chainKind,
	}
	defer func() {
		observation.Duration = time.Since(startedAt)
		p.enrichObserver.Finished(ctx, observation)
	}()
	result, failureReason := p.callEnricher(ctx, enrich.Input{Alert: normalized.Clone()})
	if err := ctx.Err(); err != nil {
		return domain.Alert{}, err
	}
	if failureReason == "" {
		var normalizeErr error
		result.Data, normalizeErr = result.Data.Normalize()
		switch {
		case result.Status != domain.EnrichStatusSucceeded &&
			result.Status != domain.EnrichStatusPartial &&
			result.Status != domain.EnrichStatusFailed &&
			result.Status != domain.EnrichStatusSkipped:
			failureReason = "invalid_enrich_status"
		case normalizeErr != nil:
			failureReason = "invalid_enrich_data"
		case domain.ValidateEnrichPayload(result.Status, result.Data) != nil:
			failureReason = "invalid_enrich_data"
		}
	}
	observation.Outcome = enrichMetricOutcome(failureReason)
	if failureReason != "" {
		p.logger.WarnContext(
			ctx,
			"alert enrich degraded",
			"bk_tenant_id", normalized.BKTenantID,
			"event_id", normalized.TriggerEventID,
			"alert_id", normalized.AlertID,
			"reason_code", failureReason,
		)
		result = failedEnrichResult()
	}
	observation.Status = result.Status
	if encoded, encodeErr := json.Marshal(result.Data); encodeErr == nil {
		observation.PayloadBytes = int64(len(encoded))
	}
	normalized.EnrichStatus = result.Status
	normalized.Enrich = result.Data.Clone()
	normalized, err = normalized.Normalize()
	if err != nil {
		return domain.Alert{}, fmt.Errorf("normalize enriched alert: %w", err)
	}
	return normalized, nil
}

func enrichMetricOutcome(failureReason string) string {
	switch failureReason {
	case "":
		return EnrichOutcomeCompleted
	case "enricher_error":
		return EnrichOutcomeError
	case "enricher_panic":
		return EnrichOutcomePanic
	case "invalid_enrich_status":
		return EnrichOutcomeInvalidStatus
	default:
		return EnrichOutcomeInvalidData
	}
}

func failedEnrichResult() enrich.Result {
	return enrich.Result{Status: domain.EnrichStatusFailed, Data: domain.JSONObject{
		"processors": json.RawMessage(`[{"enricher":{"status":"failed","value":{},"diagnostics":[{"code":"dependency_invalid","dependency":"enricher"}]}}]`),
	}}
}

func (p *Processor) callEnricher(
	ctx context.Context,
	input enrich.Input,
) (result enrich.Result, failureReason string) {
	defer func() {
		if recover() != nil {
			result = enrich.Result{}
			failureReason = "enricher_panic"
		}
	}()
	var err error
	result, err = p.enricher.Enrich(ctx, input)
	if err != nil {
		return enrich.Result{}, "enricher_error"
	}
	return result, ""
}
