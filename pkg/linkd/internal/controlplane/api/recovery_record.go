// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	"linkd/internal/domain"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/view"
	"linkd/internal/eventsource"
	"linkd/internal/lifecycle/kachook"
)

type recoveryRequest struct {
	Tenant   string `json:"bk_tenant_id"`
	EventID  string `json:"event_id"`
	Severity string `json:"severity"`
}

// queryRecoveryRecord 仅按冻结来源 Event 返回旧 KAC 面板的业务字段，不读取当前告警内容替代。
func (a *API) queryRecoveryRecord(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	var request recoveryRequest
	if decode(r, &request) != nil || domain.ValidateIdentityPart("tenant", request.Tenant, 64) != nil || request.EventID == "" || len(request.EventID) > domain.EntityIDMaxBytes || request.Severity == "" || len(request.Severity) > 32 || r.PathValue("id") == "" || len(r.PathValue("id")) > domain.EntityIDMaxBytes {
		http.Error(w, "invalid recovery identity", http.StatusBadRequest)
		return
	}
	service := a.EnrichRecords
	if service == nil || service.reader == nil || service.releases == nil {
		http.Error(w, "recovery query unavailable", http.StatusServiceUnavailable)
		return
	}
	select {
	case service.slots <- struct{}{}:
		defer func() { <-service.slots }()
	default:
		http.Error(w, "recovery query busy", http.StatusTooManyRequests)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	stored, err := service.reader.GetAlert(ctx, request.Tenant, r.PathValue("id"))
	if err != nil || stored.Alert.BKTenantID != request.Tenant || stored.Alert.AlertID != r.PathValue("id") || stored.Alert.Severity != request.Severity {
		http.Error(w, "recovery alert unavailable", http.StatusNotFound)
		return
	}
	event, err := service.reader.GetEvent(ctx, request.Tenant, request.EventID)
	if err != nil || event.Event.BKTenantID != request.Tenant || event.Event.EventID != request.EventID || event.Event.EventSourceID != stored.Alert.EventSourceID || !slices.Contains(event.Event.RelatedAlertIDs, stored.Alert.AlertID) {
		http.Error(w, "recovery event unavailable", http.StatusNotFound)
		return
	}
	release, err := service.releases.GetRelease(ctx, event.Event.EventSourceID, event.Event.EnrichSourceReleaseVersion())
	if err != nil {
		http.Error(w, "recovery release unavailable", http.StatusBadGateway)
		return
	}
	fields, err := recoveryFields(stored.Alert, event.Event, release)
	if err != nil || ctx.Err() != nil {
		http.Error(w, "recovery fact unavailable", http.StatusBadGateway)
		return
	}
	encoded, err := json.Marshal(map[string]any{"alert_id": stored.Alert.AlertID, "event_id": request.EventID, "severity": request.Severity, "fields": fields})
	if err != nil || len(encoded) > 1<<20 {
		http.Error(w, "recovery response exceeds budget", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(encoded)
}

func recoveryFields(alert domain.Alert, event domain.Event, release eventsource.Release) (map[string]any, error) {
	// 1. 与丰富摘要共用准确发布和等级校验；不能读最新发布或其他等级补结果。
	record, err := projectEnrichRecord(alert, event, release, false)
	if err != nil {
		return nil, err
	}
	var evaluation domain.EventEvaluation
	for _, candidate := range event.Evaluations {
		if candidate.Severity == record.Severity {
			evaluation = candidate
			break
		}
	}
	if evaluation.Action != domain.EventActionResolved && evaluation.Action != domain.EventActionClosed {
		return nil, fmt.Errorf("event is not a source recovery")
	}
	// 2. 仅应用持久的生效补丁，原 Event 保持不变，失败尝试不覆盖来源字段。
	enriched, _ := event.ForSeverity(record.Severity)
	patches, err := view.EffectiveEnrichPatches(enriched.Data)
	if err != nil {
		return nil, err
	}
	document, err := domain.EventDocument(event, evaluation)
	if err != nil {
		return nil, err
	}
	document, err = domain.ApplyEnrichPatches(document, patches)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	var effective domain.Event
	if err := json.Unmarshal(encoded, &effective); err != nil {
		return nil, err
	}
	// 3. 复用已有 KAC 业务字段转换；只展开发布时声明的自定义字段，不返回原始 payload。
	catalog := []string{}
	if raw, exists := effective.ExtraData["__kac_custom_fields"]; exists {
		if err := json.Unmarshal(raw, &catalog); err != nil || len(catalog) > 128 {
			return nil, fmt.Errorf("invalid recovery field catalog")
		}
	}
	var source models.SourceValues
	labels, err := json.Marshal(effective.Labels)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(labels, &source); err != nil {
		return nil, err
	}
	if source.SourceID == "" {
		source.SourceID = event.EventSourceID
	}
	if source.SourceName == "" {
		source.SourceName = event.EventSourceID
	}
	fields, err := kachook.RecoveryFields(effective, evaluation, source.SourceID, source.SourceName, catalog, func(severity string) (string, error) { return severity, nil })
	if err != nil {
		return nil, err
	}
	delete(fields, "alarm_id")
	fields["close_time"] = fields["alarm_time"]
	fields["close_reason"] = evaluation.ActionReason
	return fields, nil
}
