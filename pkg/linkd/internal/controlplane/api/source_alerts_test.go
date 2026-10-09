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
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type sourceAlertReader struct {
	*memory.Repository
	fail, cross bool
}

func (r *sourceAlertReader) ListSourceAlerts(ctx context.Context, q store.SourceAlertQuery) (store.SourceAlertPage, error) {
	if r.fail {
		return store.SourceAlertPage{}, errors.New("private database address")
	}
	page, err := r.Repository.ListSourceAlerts(ctx, q)
	if r.cross && len(page.Alerts) > 0 {
		page.Alerts[0].Alert.EventSourceID = "other"
	}
	return page, err
}

func TestSourceAlertsReadOriginalFactsAndBoundPagination(t *testing.T) {
	t.Parallel()
	reader := &sourceAlertReader{Repository: memory.New()}
	for _, source := range []string{"source", "other"} {
		for _, id := range []string{"a", "b"} {
			alert := storetest.Alert("tenant", source+id, "opening", source+id, "warning")
			alert.EventSourceID, alert.SourceEventID = source, "A123"
			alert.ExtraData = domain.JSONObject{"meta_info": json.RawMessage(`"original"`)}
			if _, err := reader.CreateAlert(t.Context(), alert); err != nil {
				t.Fatal(err)
			}
		}
	}
	api := &API{SourceAlerts: NewSourceAlerts(reader), Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}}}
	handler := api.Handler()
	base := "/api/v1/alerts?bk_tenant_id=tenant&event_source_id=source&status=active&limit=1"
	w := policyHTTP(t, handler, "GET", base, "")
	var page struct {
		Items []domain.Alert `json:"items"`
		Next  string         `json:"next"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].SourceEventID != "A123" || string(page.Items[0].ExtraData["meta_info"]) != `"original"` || page.Next == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	cursor := page.Next
	w = policyHTTP(t, handler, "GET", base+"&after="+url.QueryEscape(cursor), "")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Next != "" || page.Items[0].AlertID != "sourceb" {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, query := range []string{"", "?bk_tenant_id=tenant", "?bk_tenant_id=tenant&event_source_id=source&limit=33", "?bk_tenant_id=tenant&event_source_id=source&status=shielded", "?bk_tenant_id=tenant&event_source_id=source&unknown=1", "?bk_tenant_id=other&event_source_id=source&status=active&after=" + url.QueryEscape(cursor), "?bk_tenant_id=tenant&event_source_id=other&status=active&after=" + url.QueryEscape(cursor)} {
		if w := policyHTTP(t, handler, "GET", "/api/v1/alerts"+query, ""); w.Code != 400 {
			t.Fatal("invalid query accepted", query, w.Code)
		}
	}
	r := httptest.NewRequestWithContext(t.Context(), "GET", base, nil)
	r.Header.Set("Authorization", "Bearer worker")
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, r)
	if unauthorized.Code != 401 {
		t.Fatal("management JWT required", unauthorized.Code)
	}
	for _, mode := range []string{"fail", "cross", "busy", "cancel"} {
		reader.fail, reader.cross = mode == "fail", mode == "cross"
		if mode == "busy" {
			api.SourceAlerts.slots <- struct{}{}
			api.SourceAlerts.slots <- struct{}{}
		}
		if mode == "cancel" {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := reader.ListSourceAlerts(ctx, store.SourceAlertQuery{TenantID: "tenant", EventSourceID: "source", Limit: 1}); err == nil {
				t.Fatal("cancellation ignored")
			}
			continue
		}
		w := policyHTTP(t, handler, "GET", base, "")
		want := 502
		if mode == "busy" {
			want = 429
			<-api.SourceAlerts.slots
			<-api.SourceAlerts.slots
		}
		if w.Code != want || strings.Contains(w.Body.String(), "private") {
			t.Fatal(mode, w.Code, w.Body.String())
		}
	}
}
