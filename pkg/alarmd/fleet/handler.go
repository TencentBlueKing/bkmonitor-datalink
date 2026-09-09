// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Paging is mandatory rather than optional. An endpoint that returns everything
// by default is fine until the day it is needed most, when the list is longest.
const (
	// DefaultPageSize is what a caller gets without asking.
	DefaultPageSize = 50
	// MaxPageSize bounds what a caller can ask for.
	MaxPageSize = 500
)

// Page describes the slice of anomalies in a response. Total is how many the
// response could page over, which is not how many exist: when a replica
// truncated its list, the real count is the view's anomalies_total and the gap
// that says so.
type Page struct {
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
	Total  int `json:"total"`
}

type listResponse struct {
	View
	// Replica echoes the filter, so a caller cannot mistake a filtered response
	// for a deployment-wide one. The coverage numbers stay deployment-wide.
	Replica string `json:"replica,omitempty"`
	Page    Page   `json:"page"`
}

type detailResponse struct {
	Found      bool     `json:"found"`
	Anomaly    *Anomaly `json:"anomaly,omitempty"`
	Health     Health   `json:"health"`
	Gaps       []Gap    `json:"gaps,omitempty"`
	Complete   bool     `json:"view_complete"`
	QueryGroup string   `json:"query_group"`
}

// NewHandler mounts the object API. The routes are deliberately few: a list,
// one object, the health judgment, and the observation windows. Anything beyond
// that needs a decision, not just a handler.
//
// Windows are the one place this API writes. The write is scoped to diagnostics
// -- it selects what gets recorded, never what gets evaluated -- and it is what
// keeps the choice of observed objects out of deployment configuration, where a
// choice made during one investigation outlives it and can only be changed by a
// release. A nil store leaves the route unmounted, so a deployment that has not
// wired one is missing the route rather than serving one that cannot work.
func NewHandler(service *Service, windows *WindowStore, now func() time.Time) (http.Handler, error) {
	if service == nil {
		return nil, errors.New("alarmd fleet: handler requires a service")
	}
	if now == nil {
		now = time.Now
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/objects", func(response http.ResponseWriter, request *http.Request) {
		listObjects(response, request, service)
	})
	mux.HandleFunc("/api/objects/", func(response http.ResponseWriter, request *http.Request) {
		objectDetail(response, request, service)
	})
	if windows != nil {
		mux.HandleFunc("/api/windows", func(response http.ResponseWriter, request *http.Request) {
			observationWindows(response, request, windows, now)
		})
	}
	mux.HandleFunc("/api/health", func(response http.ResponseWriter, request *http.Request) {
		view := service.View(request.Context())
		writeJSON(response, http.StatusOK, map[string]any{
			"health":     view.Health,
			"expected":   view.Expected,
			"covered":    view.Covered,
			"determined": view.Determined,
			"unknown":    view.Unknown,
			"gaps":       view.Gaps,
		})
	})
	return mux, nil
}

func listObjects(response http.ResponseWriter, request *http.Request, service *Service) {
	offset, limit, err := paging(request)
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	view := service.View(request.Context())
	replica := request.URL.Query().Get("replica")
	if replica != "" {
		// A name that belongs to no replica has to be refused rather than
		// answered. The coverage arithmetic stays deployment-scoped on purpose,
		// so filtering by a typo would otherwise return an empty anomaly list
		// beside a full-coverage HEALTHY verdict -- a green tile for a replica
		// that does not exist.
		if !knownReplica(view, replica) {
			writeJSON(response, http.StatusBadRequest,
				map[string]string{"error": "no replica named " + replica + " is part of this deployment"})
			return
		}
		view.Anomalies = filterByReplica(view.Anomalies, replica)
	}
	total := len(view.Anomalies)
	view.Anomalies = pageOf(view.Anomalies, offset, limit)
	writeJSON(response, http.StatusOK, listResponse{
		View: view, Replica: replica, Page: Page{Offset: offset, Limit: limit, Total: total},
	})
}

func objectDetail(response http.ResponseWriter, request *http.Request, service *Service) {
	queryGroup := strings.TrimPrefix(request.URL.Path, "/api/objects/")
	if queryGroup == "" {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "query group is required"})
		return
	}
	view := service.View(request.Context())
	body := detailResponse{
		Health:     view.Health,
		Gaps:       view.Gaps,
		Complete:   view.Health != HealthUnknown,
		QueryGroup: queryGroup,
	}
	for index := range view.Anomalies {
		if view.Anomalies[index].QueryGroup == queryGroup {
			body.Found = true
			body.Anomaly = &view.Anomalies[index]
			writeJSON(response, http.StatusOK, body)
			return
		}
	}
	// Absent from an incomplete view does not mean healthy. The status says not
	// found; the body says whether that answer can be trusted.
	writeJSON(response, http.StatusNotFound, body)
}

func paging(request *http.Request) (int, int, error) {
	query := request.URL.Query()
	offset := 0
	limit := DefaultPageSize
	if raw := query.Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			return 0, 0, errors.New("offset must be a non-negative integer")
		}
		offset = parsed
	}
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return 0, 0, errors.New("limit must be a positive integer")
		}
		limit = parsed
	}
	if limit > MaxPageSize {
		limit = MaxPageSize
	}
	return offset, limit, nil
}

func pageOf(anomalies []Anomaly, offset, limit int) []Anomaly {
	if offset >= len(anomalies) {
		return []Anomaly{}
	}
	end := offset + limit
	if end > len(anomalies) {
		end = len(anomalies)
	}
	return anomalies[offset:end]
}

// windowRequest opens or closes windows. Close is a separate verb rather than a
// zero TTL, because "observe this for no time" is not a thing anyone means.
type windowRequest struct {
	QueryGroups []string `json:"query_groups"`
	OpenedBy    string   `json:"opened_by"`
	TTLSeconds  int      `json:"ttl_seconds"`
	Close       bool     `json:"close"`
}

func observationWindows(response http.ResponseWriter, request *http.Request, windows *WindowStore, now func() time.Time) {
	switch request.Method {
	case http.MethodGet:
		open, err := windows.Load(request.Context(), now())
		if err != nil {
			writeJSON(response, http.StatusServiceUnavailable, map[string]string{"error": "window store unavailable"})
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"windows": open, "max_open": MaxOpenWindows, "max_ttl_seconds": int(MaxWindowTTL.Seconds())})
	case http.MethodPost:
		var body windowRequest
		if err := json.NewDecoder(http.MaxBytesReader(response, request.Body, 64<<10)).Decode(&body); err != nil {
			writeJSON(response, http.StatusBadRequest, map[string]string{"error": "body must be a window request"})
			return
		}
		at := now()
		var (
			open []Window
			err  error
		)
		if body.Close {
			open, err = windows.Close(request.Context(), body.QueryGroups, at)
		} else {
			open, err = windows.Open(request.Context(), body.QueryGroups, body.OpenedBy,
				time.Duration(body.TTLSeconds)*time.Second, at)
		}
		if err != nil {
			// The rejections here are all about what the caller asked for --
			// an unknown identity, too many objects, too long a window -- so
			// the reason is the caller's to see. A store failure surfaces as
			// the same message, which is the one case worth improving later.
			writeJSON(response, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"windows": open, "max_open": MaxOpenWindows, "max_ttl_seconds": int(MaxWindowTTL.Seconds())})
	default:
		writeJSON(response, http.StatusMethodNotAllowed, map[string]string{"error": "GET to read windows, POST to open or close one"})
	}
}

// knownReplica reports whether the deployment contains a replica by this name.
// A replica that published nothing is still part of the deployment: it is named
// by the gap that says so, and asking about it is a legitimate question with the
// answer "it reported nothing".
func knownReplica(view View, replica string) bool {
	for _, contributor := range view.Replicas {
		if contributor == replica {
			return true
		}
	}
	for _, gap := range view.Gaps {
		if gap.Replica == replica {
			return true
		}
	}
	return false
}

func filterByReplica(anomalies []Anomaly, replica string) []Anomaly {
	filtered := make([]Anomaly, 0, len(anomalies))
	for _, anomaly := range anomalies {
		if anomaly.Replica == replica {
			filtered = append(filtered, anomaly)
		}
	}
	return filtered
}

func writeJSON(response http.ResponseWriter, status int, body any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(body)
}
