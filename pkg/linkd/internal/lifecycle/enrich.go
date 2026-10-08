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
	"linkd/internal/store"
)

// EventEnricher 在生命周期和策略裁决前为每条 Event 执行只读、可重试的丰富。
type EventEnricher interface {
	Enrich(ctx context.Context, input enrich.Input) (enrich.Result, error)
}

// enrichEvent 先冻结结果再允许计划/策略产生副作用。CAS 失败交由外层重读，已提交结果绝不重算。
func (p *Processor) enrichEvent(ctx context.Context, stored store.StoredEvent) (store.StoredEvent, error) {
	event, err := stored.Event.Normalize()
	if err != nil {
		return store.StoredEvent{}, fmt.Errorf("normalize event before enrich: %w", err)
	}
	if event.EnrichStatus != domain.EnrichStatusPending {
		return stored, nil
	}
	startedAt := time.Now()
	chainKind := enrich.ChainUnknown
	if classifier, ok := p.enricher.(EnrichRouteClassifier); ok {
		chainKind = classifier.EnrichChainKind(event.EventSourceID)
	}
	p.enrichObserver.Started(ctx, event.EventSourceID)
	observation := EnrichObservation{EventSourceID: event.EventSourceID, Status: domain.EnrichStatusFailed, Outcome: EnrichOutcomeError, ChainKind: chainKind}
	defer func() { observation.Duration = time.Since(startedAt); p.enrichObserver.Finished(ctx, observation) }()
	result, failureReason := p.callEnricher(ctx, enrich.Input{Event: event.Clone()})
	if result.ChainKind != "" {
		observation.ChainKind = result.ChainKind
	}
	if err := ctx.Err(); err != nil {
		return store.StoredEvent{}, err
	}
	at, err := p.now()
	if err != nil {
		return store.StoredEvent{}, err
	}
	digest := result.ConfigDigest
	// 依赖初始化或自定义执行器失败时没有可信配置摘要；明确记录 unavailable，不伪造配置身份。
	if digest == "" {
		digest = "unavailable"
	}
	if len(digest) > 128 {
		digest = "unavailable"
		if failureReason == "" {
			failureReason = "invalid_enrich_data"
		}
	}
	snapshot := domain.EventEnrichment{EnrichStatus: result.Status, Enrich: result.Data, EnrichedAt: &at, EnrichConfigDigest: digest}
	if failureReason == "" {
		switch {
		case !result.Status.Valid() || result.Status == domain.EnrichStatusPending:
			failureReason = "invalid_enrich_status"
		default:
			var err error
			snapshot, err = snapshot.Normalize(event.Evaluations)
			if err != nil {
				failureReason = "invalid_enrich_data"
			}
		}
	}
	observation.Outcome = enrichMetricOutcome(failureReason)
	if failureReason != "" {
		p.logger.WarnContext(ctx, "event enrich degraded", "bk_tenant_id", event.BKTenantID, "event_id", event.EventID, "reason_code", failureReason)
		result = failedEnrichResult(event)
		snapshot = domain.EventEnrichment{EnrichStatus: result.Status, Enrich: result.Data, EnrichedAt: &at, EnrichConfigDigest: digest}
	}
	observation.Status = snapshot.EnrichStatus
	if encoded, err := json.Marshal(snapshot.Enrich); err == nil {
		observation.PayloadBytes = int64(len(encoded))
	}
	return p.repository.CompareAndSetEventEnrichment(ctx, event.BKTenantID, event.EventID, stored.Version, snapshot)
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

func failedEnrichResult(event domain.Event) enrich.Result {
	result := enrich.Result{Status: domain.EnrichStatusFailed}
	for _, evaluation := range event.Evaluations {
		result.Data.Evaluations = append(result.Data.Evaluations, domain.EvaluationEnrich{Severity: evaluation.Severity, Status: domain.EnrichStatusFailed, Data: domain.JSONObject{
			"processors": json.RawMessage(`[{"enricher":{"status":"failed","value":{},"diagnostics":[{"code":"dependency_invalid","dependency":"enricher"}]}}]`),
		}})
	}
	return result
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
		return result, "enricher_error"
	}
	return result, ""
}
