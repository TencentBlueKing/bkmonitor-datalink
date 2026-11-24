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
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// SourceAlerts 提供来源轮询需要的原始告警快照，独立限制查询并发，不执行生命周期写入。
type SourceAlerts struct {
	reader store.SourceAlertReader
	slots  chan struct{}
}

// NewSourceAlerts 注入存储中立的只读端口；底层连接由调用方释放。
func NewSourceAlerts(reader store.SourceAlertReader) *SourceAlerts {
	return &SourceAlerts{reader: reader, slots: make(chan struct{}, 2)}
}

func (a *API) listSourceAlerts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if err := policyQuery(r, "bk_tenant_id", "event_source_id", "status", "after", "limit"); err != nil {
		http.Error(w, "invalid source alert query", http.StatusBadRequest)
		return
	}
	values := r.URL.Query()
	q := store.SourceAlertQuery{TenantID: values.Get("bk_tenant_id"), EventSourceID: values.Get("event_source_id"), Status: domain.AlertStatus(values.Get("status")), Limit: 32}
	if raw := values.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		q.Limit = n
	}
	scope := []string{q.TenantID, q.EventSourceID, string(q.Status)}
	if raw := values.Get("after"); raw != "" {
		if len(raw) > 4096 {
			http.Error(w, "invalid source alert cursor", http.StatusBadRequest)
			return
		}
		var parts []string
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil || json.Unmarshal(decoded, &parts) != nil || len(parts) != 4 || !slices.Equal(parts[:3], scope) {
			http.Error(w, "invalid source alert cursor", http.StatusBadRequest)
			return
		}
		q.After = parts[3]
	}
	if err := q.Validate(); err != nil {
		http.Error(w, "invalid source alert query", http.StatusBadRequest)
		return
	}
	service := a.SourceAlerts
	if service == nil || service.reader == nil {
		http.Error(w, "source alert query unavailable", http.StatusServiceUnavailable)
		return
	}
	select {
	case service.slots <- struct{}{}:
		defer func() { <-service.slots }()
	default:
		http.Error(w, "source alert query busy", http.StatusTooManyRequests)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	page, err := service.reader.ListSourceAlerts(ctx, q)
	if err != nil {
		http.Error(w, "source alert query failed", http.StatusBadGateway)
		return
	}
	items := make([]domain.Alert, 0, len(page.Alerts))
	last := q.After
	for _, item := range page.Alerts {
		if !q.Matches(item.Alert) || item.Alert.AlertID <= last || len(items) == q.Limit {
			http.Error(w, "invalid source alert page", http.StatusBadGateway)
			return
		}
		last = item.Alert.AlertID
		items = append(items, item.Alert)
	}
	next := ""
	if page.Next != "" {
		if len(items) == 0 || page.Next != last {
			http.Error(w, "invalid source alert page", http.StatusBadGateway)
			return
		}
		raw, _ := json.Marshal(append(scope, page.Next))
		next = base64.RawURLEncoding.EncodeToString(raw)
	}
	payload, err := json.Marshal(struct {
		Items []domain.Alert `json:"items"`
		Next  string         `json:"next"`
	}{items, next})
	if err != nil || len(payload) > 8<<20 {
		http.Error(w, "source alert page exceeds response budget", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(payload)
}
