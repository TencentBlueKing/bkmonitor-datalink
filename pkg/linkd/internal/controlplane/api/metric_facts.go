package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"slices"
	"time"

	"linkd/internal/domain"
	"linkd/internal/enrich"
)

// queryMetricFacts 只返回内置告警准确事件的查询事实，不读取策略或执行丰富。
// 默认固定为 TriggerEventID；显式 EventID 只允许同告警同等级的恢复/关闭事件。
func (a *API) queryMetricFacts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	var request recoveryRequest
	id := r.PathValue("id")
	if decode(r, &request) != nil || domain.ValidateIdentityPart("tenant", request.Tenant, 64) != nil || id == "" || len(id) > domain.EntityIDMaxBytes || request.Severity == "" || len(request.Severity) > 32 || len(request.EventID) > domain.EntityIDMaxBytes {
		http.Error(w, "invalid metric fact identity", http.StatusBadRequest)
		return
	}
	service := a.EnrichRecords
	if service == nil || service.reader == nil {
		http.Error(w, "metric facts unavailable", http.StatusServiceUnavailable)
		return
	}
	select {
	case service.slots <- struct{}{}:
		defer func() { <-service.slots }()
	default:
		http.Error(w, "metric facts busy", http.StatusTooManyRequests)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	// 1. 核对租户、内置来源及告警等级，不能把其他来源的同名字段解释为策略执行引用。
	stored, err := service.reader.GetAlert(ctx, request.Tenant, id)
	if err != nil || stored.Alert.BKTenantID != request.Tenant || stored.Alert.AlertID != id || stored.Alert.EventSourceID != "built_in_bk" || stored.Alert.Severity != request.Severity {
		http.Error(w, "metric alert unavailable", http.StatusNotFound)
		return
	}
	eventID := stored.Alert.TriggerEventID
	if request.EventID != "" {
		eventID = request.EventID
	}
	if eventID == "" {
		http.Error(w, "opening event unavailable", http.StatusNotFound)
		return
	}
	// 2. 仅读取准确事件；后续重复触发的 LatestEventID 不替换 opening 事实。
	storedEvent, err := service.reader.GetEvent(ctx, request.Tenant, eventID)
	event := storedEvent.Event
	if err != nil || event.BKTenantID != request.Tenant || event.EventID != eventID || event.EventSourceID != stored.Alert.EventSourceID || !slices.Contains(event.RelatedAlertIDs, id) {
		http.Error(w, "metric event unavailable", http.StatusNotFound)
		return
	}
	if request.EventID != "" {
		valid := false
		for _, evaluation := range event.Evaluations {
			if evaluation.Severity == request.Severity && (evaluation.Action == domain.EventActionResolved || evaluation.Action == domain.EventActionClosed) {
				valid = true
			}
		}
		if !valid {
			http.Error(w, "metric recovery unavailable", http.StatusNotFound)
			return
		}
	}
	ids, diagnostics := enrich.ValidateRequiredIDs(event)
	if len(diagnostics) != 0 {
		http.Error(w, "metric execution reference unavailable", http.StatusUnprocessableEntity)
		return
	}
	// 3. 不返回 payload、正文或完整策略；JSON 保留原维度类型，响应具有硬字节上限。
	result := map[string]any{
		"bk_tenant_id": request.Tenant, "alert_id": id, "event_id": eventID,
		"event_source_id": event.EventSourceID, "severity": request.Severity,
		"strategy_id": ids.StrategyID, "strategy_version": ids.StrategyVersion, "bk_biz_id": ids.BizID,
		"dimensions": event.Dimensions, "occurred_at": event.OccurredAt,
	}
	if value, exists := event.Labels["item_id"]; exists {
		number, ok := value.NumberValue()
		if !ok || number <= 0 || math.Trunc(number) != number || number > math.MaxInt64 {
			http.Error(w, "invalid metric item identity", http.StatusUnprocessableEntity)
			return
		}
		result["item_id"] = int64(number)
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 1<<20 || ctx.Err() != nil {
		http.Error(w, "metric fact response unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(encoded)
}
