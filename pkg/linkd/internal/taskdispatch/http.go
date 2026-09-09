// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package taskdispatch

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"linkd/internal/config"
	"linkd/internal/eventsource"
)

// API 提供来源管理和 worker 协议；两类请求使用不同 token。
type API struct {
	Lifecycle  config.LifecycleConfig
	Sources    *eventsource.Service
	Controller *Controller
	Config     config.DispatchConfig
}

// Handler 创建有身份校验的正式接口。
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/event-sources", a.list)
	mux.HandleFunc("GET /api/v1/event-sources/{id}", a.get)
	mux.HandleFunc("PUT /api/v1/event-sources/{id}", a.put)
	mux.HandleFunc("DELETE /api/v1/event-sources/{id}", a.delete)
	mux.HandleFunc("GET /api/v1/event-sources/{id}/releases/{version}", a.release)
	mux.HandleFunc("GET /api/v1/runtime", a.status)
	mux.HandleFunc("POST /internal/heartbeat", a.beat)
	mux.HandleFunc("GET /internal/releases/{id}/{version}", a.workerRelease)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := a.Config.APIToken
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			token = a.Config.WorkerToken
		}
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(provided)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}

func output(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if e := json.NewEncoder(w).Encode(value); e != nil {
		return
	}
}

func failure(w http.ResponseWriter, e error) {
	code := http.StatusBadRequest
	if errors.Is(e, eventsource.ErrNotFound) {
		code = 404
	}
	if errors.Is(e, eventsource.ErrConflict) {
		code = 409
	}
	http.Error(w, http.StatusText(code), code)
}

func decode(r *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, (1<<20)+1))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	var extra any
	if !errors.Is(d.Decode(&extra), io.EOF) {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

// includeSecrets 仅允许已通过管理 token 鉴权的显式读取；默认接口继续脱敏。
func includeSecrets(w http.ResponseWriter, r *http.Request) (bool, error) {
	raw := r.URL.Query().Get("include_secrets")
	if raw == "" || raw == "false" {
		return false, nil
	}
	if raw != "true" {
		return false, fmt.Errorf("invalid include_secrets")
	}
	w.Header().Set("Cache-Control", "no-store")
	return true, nil
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	full, err := includeSecrets(w, r)
	if err != nil {
		failure(w, err)
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var e error
		limit, e = strconv.Atoi(raw)
		if e != nil {
			failure(w, e)
			return
		}
	}
	rs, e := a.Sources.List(r.Context(), r.URL.Query().Get("after"), limit)
	if e != nil {
		failure(w, e)
		return
	}
	if !full {
		for i := range rs {
			rs[i] = rs[i].Redacted()
		}
	}
	output(w, rs)
}

func (a *API) get(w http.ResponseWriter, r *http.Request) {
	full, err := includeSecrets(w, r)
	if err != nil {
		failure(w, err)
		return
	}
	v, e := a.Sources.Get(r.Context(), r.PathValue("id"))
	if e != nil {
		failure(w, e)
		return
	}
	if !full {
		v = v.Redacted()
	}
	output(w, v)
}

// Mutation 省略 security 时保持原凭据，显式提交则整体替换认证材料。
type Mutation struct {
	Expected int64              `json:"expected_revision"`
	Spec     config.EventSource `json:"spec"`
}

func (a *API) put(w http.ResponseWriter, r *http.Request) {
	var m Mutation
	if e := decode(r, &m); e != nil {
		failure(w, e)
		return
	}
	if m.Spec.EventSourceID != r.PathValue("id") {
		failure(w, fmt.Errorf("source id mismatch"))
		return
	}
	if old, e := a.Sources.Get(r.Context(), m.Spec.EventSourceID); e == nil && m.Spec.Storage.Kafka.Security.Protocol == "" && m.Spec.Storage.Kafka.Security.SASL == nil {
		m.Spec.Storage.Kafka.Security = old.Spec.Storage.Kafka.Security
	}
	record, e := a.Sources.Apply(r.Context(), m.Spec, m.Expected, false, "api")
	if e != nil {
		failure(w, e)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	output(w, record.Redacted())
}

func (a *API) delete(w http.ResponseWriter, r *http.Request) {
	var m struct {
		Expected int64 `json:"expected_revision"`
	}
	if e := decode(r, &m); e != nil {
		failure(w, e)
		return
	}
	old, e := a.Sources.Get(r.Context(), r.PathValue("id"))
	if e == nil {
		old, e = a.Sources.Apply(r.Context(), old.Spec, m.Expected, true, "api")
	}
	if e != nil {
		failure(w, e)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	output(w, old.Redacted())
}

func (a *API) release(w http.ResponseWriter, r *http.Request) {
	v, e := strconv.ParseInt(r.PathValue("version"), 10, 64)
	if e != nil {
		failure(w, e)
		return
	}
	rel, e := a.Sources.GetRelease(r.Context(), r.PathValue("id"), v)
	if e != nil {
		failure(w, e)
		return
	}
	rel.Spec = rel.Spec.Redacted()
	output(w, rel)
}

func (a *API) status(w http.ResponseWriter, r *http.Request) {
	s, e := a.Controller.Snapshot(r.Context())
	if e != nil {
		failure(w, e)
		return
	}
	routes := map[string]map[string]string{}
	for _, status := range s.Statuses {
		lc := a.Lifecycle.ForSource(a.Config.Deployment, status.Source)
		routes[status.Source] = map[string]string{"stream": lc.Signal.Stream, "mailbox_prefix": lc.Mailbox.KeyPrefix, "lock_prefix": lc.Lock.KeyPrefix}
	}
	output(w, struct {
		State
		Routes map[string]map[string]string `json:"routes"`
	}{s, routes})
}

func (a *API) beat(w http.ResponseWriter, r *http.Request) {
	var h Heartbeat
	if e := decode(r, &h); e != nil {
		failure(w, e)
		return
	}
	if e := config.ValidateLabels(h.Worker.Labels); e != nil {
		failure(w, e)
		return
	}
	tasks, e := a.Controller.Beat(r.Context(), h)
	if e != nil {
		failure(w, e)
		return
	}
	output(w, tasks)
}

func (a *API) workerRelease(w http.ResponseWriter, r *http.Request) {
	state, e := a.Controller.Snapshot(r.Context())
	if e != nil {
		failure(w, e)
		return
	}
	id := r.URL.Query().Get("task")
	t, ok := state.Tasks[id]
	version, e := strconv.ParseInt(r.PathValue("version"), 10, 64)
	if e != nil || !ok || t.Worker != r.Header.Get("X-Worker-ID") || t.Source != r.PathValue("id") || t.Version != version || t.Phase == "stopped" {
		http.Error(w, "assignment required", http.StatusForbidden)
		return
	}
	rel, e := a.Sources.GetRelease(r.Context(), t.Source, t.Version)
	if e != nil {
		failure(w, e)
		return
	}
	output(w, rel)
}
