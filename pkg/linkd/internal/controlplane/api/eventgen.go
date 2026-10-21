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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"linkd/internal/cleaner"
	"linkd/internal/config"
	"linkd/internal/consume"
	"linkd/internal/enrich/preview"
	"linkd/internal/eventgen"
)

func (a *API) eventgenRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/eventgen/sample/preview", a.eventgenSamplePreview)
	mux.HandleFunc("POST /internal/eventgen/heartbeat", a.eventgenHeartbeat)
	mux.HandleFunc("GET /api/v1/eventgen/tasks", a.eventgenTasks)
	mux.HandleFunc("GET /api/v1/eventgen/tasks/{id}", a.eventgenTask)
	mux.HandleFunc("PUT /api/v1/eventgen/tasks/{id}", a.eventgenApply)
	mux.HandleFunc("DELETE /api/v1/eventgen/tasks/{id}", a.eventgenDelete)
	mux.HandleFunc("POST /api/v1/eventgen/tasks/{id}/start", a.eventgenStart)
	mux.HandleFunc("GET /api/v1/eventgen/runs", a.eventgenRuns)
	mux.HandleFunc("GET /api/v1/eventgen/runs/{id}", a.eventgenRun)
	mux.HandleFunc("POST /api/v1/eventgen/runs/{id}/control", a.eventgenControl)
	mux.HandleFunc("POST /api/v1/eventgen/preview", a.eventgenPreview)
	mux.HandleFunc("POST /api/v1/eventgen/import", a.eventgenImport)
}

func eventgenDecode(r *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, (8<<20)+1))
	d.DisallowUnknownFields()
	d.UseNumber()
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("invalid eventgen request")
	}
	var extra any
	if !errors.Is(d.Decode(&extra), io.EOF) {
		return fmt.Errorf("invalid eventgen request")
	}
	return nil
}

func (a *API) eventgenReady(w http.ResponseWriter, r *http.Request) (string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if a.Eventgen == nil {
		http.Error(w, "eventgen unavailable", http.StatusServiceUnavailable)
		return "", false
	}
	tenant := r.URL.Query().Get("bk_tenant_id")
	if tenant == "" || len(tenant) > 64 {
		http.Error(w, "bk_tenant_id is required", 400)
		return "", false
	}
	return tenant, true
}

func eventgenOutput(w http.ResponseWriter, v any, err error) {
	if err == nil {
		output(w, v)
		return
	}
	code := 400
	if errors.Is(err, eventgen.ErrNotFound) {
		code = 404
	}
	if errors.Is(err, eventgen.ErrConflict) {
		code = 409
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	output(w, map[string]any{"error": map[string]string{"message": err.Error()}})
}

func eventgenPage(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if n < 1 || n > 100 {
		n = 100
	}
	return n
}

func (a *API) eventgenTasks(w http.ResponseWriter, r *http.Request) {
	tenant, ok := a.eventgenReady(w, r)
	if !ok {
		return
	}
	v, e := a.Eventgen.ListPlans(r.Context(), tenant, r.URL.Query().Get("after"), eventgenPage(r))
	eventgenOutput(w, v, e)
}

func (a *API) eventgenTask(w http.ResponseWriter, r *http.Request) {
	tenant, ok := a.eventgenReady(w, r)
	if !ok {
		return
	}
	v, e := a.Eventgen.GetPlan(r.Context(), tenant, r.PathValue("id"))
	eventgenOutput(w, v, e)
}

func (a *API) eventgenApply(w http.ResponseWriter, r *http.Request) {
	tenant, ok := a.eventgenReady(w, r)
	if !ok {
		return
	}
	var request struct {
		Revision int64           `json:"revision"`
		Config   eventgen.Config `json:"config"`
	}
	if e := eventgenDecode(r, &request); e != nil {
		eventgenOutput(w, nil, e)
		return
	}
	v, e := a.Eventgen.ApplyPlan(r.Context(), tenant, r.PathValue("id"), request.Revision, request.Config)
	eventgenOutput(w, v, e)
}

func (a *API) eventgenDelete(w http.ResponseWriter, r *http.Request) {
	tenant, ok := a.eventgenReady(w, r)
	if !ok {
		return
	}
	revision, _ := strconv.ParseInt(r.URL.Query().Get("revision"), 10, 64)
	eventgenOutput(w, map[string]bool{"deleted": true}, a.Eventgen.DeletePlan(r.Context(), tenant, r.PathValue("id"), revision))
}

func (a *API) eventgenStart(w http.ResponseWriter, r *http.Request) {
	tenant, ok := a.eventgenReady(w, r)
	if !ok {
		return
	}
	var c eventgen.Command
	if e := eventgenDecode(r, &c); e != nil {
		eventgenOutput(w, nil, e)
		return
	}
	v, e := a.Eventgen.Start(r.Context(), tenant, r.PathValue("id"), c)
	eventgenOutput(w, v, e)
}

func (a *API) eventgenRuns(w http.ResponseWriter, r *http.Request) {
	tenant, ok := a.eventgenReady(w, r)
	if !ok {
		return
	}
	v, e := a.Eventgen.ListRuns(r.Context(), tenant, r.URL.Query().Get("after"), eventgenPage(r))
	eventgenOutput(w, v, e)
}

func (a *API) eventgenRun(w http.ResponseWriter, r *http.Request) {
	tenant, ok := a.eventgenReady(w, r)
	if !ok {
		return
	}
	v, e := a.Eventgen.GetRun(r.Context(), tenant, r.PathValue("id"))
	eventgenOutput(w, v, e)
}

func (a *API) eventgenControl(w http.ResponseWriter, r *http.Request) {
	tenant, ok := a.eventgenReady(w, r)
	if !ok {
		return
	}
	var c eventgen.Command
	if e := eventgenDecode(r, &c); e != nil {
		eventgenOutput(w, nil, e)
		return
	}
	v, e := a.Eventgen.Control(r.Context(), tenant, r.PathValue("id"), c)
	eventgenOutput(w, v, e)
}

func (a *API) eventgenHeartbeat(w http.ResponseWriter, r *http.Request) {
	if a.Eventgen == nil {
		http.Error(w, "eventgen unavailable", http.StatusServiceUnavailable)
		return
	}
	var h eventgen.Heartbeat
	if e := eventgenDecode(r, &h); e != nil {
		eventgenOutput(w, nil, e)
		return
	}
	v, e := a.Eventgen.Poll(r.Context(), h)
	eventgenOutput(w, v, e)
}

func (a *API) eventgenPreview(w http.ResponseWriter, r *http.Request) {
	tenant, ok := a.eventgenReady(w, r)
	if !ok {
		return
	}
	var request struct {
		Config  eventgen.Config `json:"config"`
		Count   int             `json:"count"`
		StartAt time.Time       `json:"start_at"`
	}
	if e := eventgenDecode(r, &request); e != nil {
		eventgenOutput(w, nil, e)
		return
	}
	if request.Config.TenantID != tenant {
		eventgenOutput(w, nil, fmt.Errorf("tenant mismatch"))
		return
	}
	v, e := eventgen.Preview(request.Config, request.StartAt, request.Count)
	eventgenOutput(w, v, e)
}

func (a *API) eventgenImport(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.eventgenReady(w, r); !ok {
		return
	}
	var request struct {
		CSV     string            `json:"csv"`
		Columns map[string]string `json:"columns"`
	}
	if e := eventgenDecode(r, &request); e != nil {
		eventgenOutput(w, nil, e)
		return
	}
	v, e := eventgen.ImportCSV(request.CSV, request.Columns)
	eventgenOutput(w, v, e)
}

// eventgenSamplePreview 只在显式预览请求中执行真实 Cleaner 和 Enrich，不写业务实体。
func (a *API) eventgenSamplePreview(w http.ResponseWriter, r *http.Request) {
	tenant, ok := a.eventgenReady(w, r)
	if !ok {
		return
	}
	var request struct {
		SourceID string          `json:"event_source_id"`
		Payload  json.RawMessage `json:"payload"`
		Enrich   bool            `json:"enrich"`
	}
	if e := eventgenDecode(r, &request); e != nil {
		eventgenOutput(w, nil, e)
		return
	}
	sources, e := a.Eventgen.ResolveSources(r.Context(), eventgen.Config{TenantID: tenant, Scenarios: []eventgen.Scenario{{SourceID: request.SourceID}}})
	if e != nil {
		eventgenOutput(w, nil, e)
		return
	}
	severity := config.DefaultSeverityConfig()
	if a.EventgenSeverity != nil {
		severity = a.EventgenSeverity()
	}
	mapper, e := cleaner.NewMapper(sources[request.SourceID], severity)
	if e != nil {
		eventgenOutput(w, nil, fmt.Errorf("source cleaner is unavailable"))
		return
	}
	normalized, e := mapper.MapMessage(r.Context(), consume.Message{ID: "eventgen-preview", TenantID: tenant, Body: request.Payload, EnqueuedAt: time.Now().UTC()})
	if e != nil {
		eventgenOutput(w, nil, fmt.Errorf("sample could not be normalized"))
		return
	}
	response := map[string]any{"event": normalized}
	if request.Enrich {
		if a.Previewer == nil {
			eventgenOutput(w, nil, fmt.Errorf("enrich preview is unavailable"))
			return
		}
		body, e := json.Marshal(normalized)
		if e != nil {
			eventgenOutput(w, nil, e)
			return
		}
		result, e := a.Previewer.Preview(r.Context(), preview.Request{BKTenantID: tenant, EventSourceID: request.SourceID, Input: preview.Input{Event: body}})
		if e != nil {
			eventgenOutput(w, nil, fmt.Errorf("enrich preview failed"))
			return
		}
		response["enrich"] = result
	}
	eventgenOutput(w, response, nil)
}
