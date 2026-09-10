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
	"sort"
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
	Replica  string `json:"replica,omitempty"`
	Strategy string `json:"strategy,omitempty"`
	Business string `json:"business,omitempty"`
	// Applied says a filter narrowed this response. An empty table means
	// something different when it was filtered, and the caller cannot tell the
	// two apart from the rows alone.
	Applied bool    `json:"filtered"`
	Summary Summary `json:"summary"`
	// StallAfterSeconds is the budget an object's rounds have to finish in before
	// the list calls it stalled. It is reported rather than assumed by the reader
	// so the flag can be checked against the deployment that produced it instead
	// of against a number someone remembers.
	StallAfterSeconds int  `json:"stall_after_seconds,omitempty"`
	Page              Page `json:"page"`
}

// Count is one value and how many anomalies carry it.
type Count struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// Summary groups the anomalies this request is about.
//
// It exists because the list alone cannot answer the question a long list
// immediately raises: is this one problem repeated, or many separate ones. The
// counts are exact over the whole list rather than over the returned page --
// the service holds every anomaly before paging, so no extra read is needed and
// no partial evidence is presented as a whole. What the list itself cannot
// cover is still reported the same way: a truncated snapshot shows up in the
// gaps and in anomalies_total, and these counts inherit that limit.
type Summary struct {
	ByKind   []Count `json:"by_kind"`
	ByReason []Count `json:"by_reason"`
	// ByFailure is usually the most informative of the three. A completion kind
	// is shared by everything that ended badly, so counting it answers "how
	// many" and not "how many of what"; the failure classification separates one
	// broken dependency from a scattering of unrelated problems.
	ByFailure []Count `json:"by_failure"`
	ByReplica []Count `json:"by_replica"`
	// Stalled counts the objects that are stuck rather than merely degraded. The
	// other three say how badly the last round went; this one says the rounds
	// stopped ending, which is the only one of the four that cannot resolve on
	// its own.
	Stalled int `json:"stalled"`
}

func summarize(anomalies []Anomaly) Summary {
	kinds := map[string]int{}
	reasons := map[string]int{}
	failures := map[string]int{}
	replicas := map[string]int{}
	stalled := 0
	for _, anomaly := range anomalies {
		if anomaly.Stalled {
			stalled++
		}
		kinds[anomaly.Kind]++
		if anomaly.ReasonCode != "" {
			reasons[anomaly.ReasonCode]++
		}
		if anomaly.Failure != nil && anomaly.Failure.Category != "" {
			failures[anomaly.Failure.Category]++
		}
		replicas[anomaly.Replica]++
	}
	return Summary{
		ByKind: rank(kinds), ByReason: rank(reasons),
		ByFailure: rank(failures), ByReplica: rank(replicas),
		Stalled: stalled,
	}
}

// MarkStalled flags the objects whose rounds have not finished for longer than
// stallAfter, the deployment's own budget for terminating a Slot that cannot
// complete. Past that budget the object is not progressing slowly, it is not
// progressing at all, and nothing left in the deployment will end the round for
// it.
//
// Blocked rounds are excluded on purpose: they say a round never started, which
// an ordinary lease handover produces, and counting them would put a permanent
// label on a transient event. A zero budget turns the flag off rather than
// marking everything, so a deployment that has not wired one shows no flag
// instead of a wrong one.
func MarkStalled(anomalies []Anomaly, at time.Time, stallAfter time.Duration) {
	if stallAfter <= 0 {
		return
	}
	for index := range anomalies {
		anomalies[index].Stalled = failedExecution(anomalies[index].ReasonCode) &&
			at.Sub(anomalies[index].Since) > stallAfter
	}
}

// rank orders by count and then by value, so equal counts do not reorder
// between two reads of an unchanged deployment.
func rank(counts map[string]int) []Count {
	ranked := make([]Count, 0, len(counts))
	for value, count := range counts {
		ranked = append(ranked, Count{Value: value, Count: count})
	}
	sort.Slice(ranked, func(left, right int) bool {
		if ranked[left].Count == ranked[right].Count {
			return ranked[left].Value < ranked[right].Value
		}
		return ranked[left].Count > ranked[right].Count
	})
	return ranked
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
// stallAfter is how long an object's rounds may keep failing to finish before
// the API calls it stalled; it comes from the deployment's own replay budget so
// the flag means "past the point this deployment promised to end the round",
// not a number chosen here. Zero disables the flag.
// series is optional: a deployment that has not been told where its own metrics
// live cannot draw curves, and that must cost it the curves only -- the
// judgment and the object list are computed from the control plane and stay
// available either way.
func NewHandler(
	service *Service,
	windows *WindowStore,
	now func() time.Time,
	stallAfter time.Duration,
	series RangeProvider,
) (http.Handler, error) {
	if service == nil {
		return nil, errors.New("alarmd fleet: handler requires a service")
	}
	if now == nil {
		now = time.Now
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/objects", func(response http.ResponseWriter, request *http.Request) {
		listObjects(response, request, service, now, stallAfter)
	})
	mux.HandleFunc("/api/objects/", func(response http.ResponseWriter, request *http.Request) {
		objectDetail(response, request, service, now, stallAfter)
	})
	if windows != nil {
		mux.HandleFunc("/api/windows", func(response http.ResponseWriter, request *http.Request) {
			observationWindows(response, request, windows, now)
		})
	}
	if series != nil {
		mux.HandleFunc("/api/series", func(response http.ResponseWriter, request *http.Request) {
			seriesRange(response, request, series, now)
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

func listObjects(response http.ResponseWriter, request *http.Request, service *Service,
	now func() time.Time, stallAfter time.Duration) {
	offset, limit, err := paging(request)
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	view := service.View(request.Context())
	// Marked before filtering so a filtered response reports the same flag for the
	// same object as an unfiltered one.
	MarkStalled(view.Anomalies, now(), stallAfter)
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
	// Strategy and business are not refused when nothing matches, unlike an
	// unknown replica: the deployment's replicas are a short knowable list, while
	// a strategy that simply has no anomalies right now is the ordinary answer to
	// a reasonable question. The response says a filter was applied so an empty
	// table is not read as "nothing is wrong anywhere".
	strategy := request.URL.Query().Get("strategy")
	if strategy != "" {
		view.Anomalies = filterByStrategy(view.Anomalies, strategy)
	}
	business := request.URL.Query().Get("business")
	if business != "" {
		view.Anomalies = filterByBusiness(view.Anomalies, business)
	}
	total := len(view.Anomalies)
	// Counted over the whole list this request is about, before it is cut into a
	// page. A reader's first question is whether a long list is one problem or
	// many, and counting only the visible page would answer it with whatever
	// happened to be on screen.
	summary := summarize(view.Anomalies)
	view.Anomalies = pageOf(view.Anomalies, offset, limit)
	writeJSON(response, http.StatusOK, listResponse{
		Summary: summary,
		View:    view, Replica: replica, Strategy: strategy, Business: business,
		Applied:           replica != "" || strategy != "" || business != "",
		StallAfterSeconds: int(stallAfter / time.Second),
		Page:              Page{Offset: offset, Limit: limit, Total: total},
	})
}

func objectDetail(response http.ResponseWriter, request *http.Request, service *Service,
	now func() time.Time, stallAfter time.Duration) {
	queryGroup := strings.TrimPrefix(request.URL.Path, "/api/objects/")
	if queryGroup == "" {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "query group is required"})
		return
	}
	view := service.View(request.Context())
	MarkStalled(view.Anomalies, now(), stallAfter)
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

// filterByStrategy keeps objects serving the given strategy. One object can
// serve several, so a match on any of them keeps it.
func filterByStrategy(anomalies []Anomaly, strategyID string) []Anomaly {
	return filterByStrategyField(anomalies, func(s StrategyRef) bool { return s.StrategyID == strategyID })
}

// filterByBusiness keeps objects serving any strategy of the given business.
func filterByBusiness(anomalies []Anomaly, businessID string) []Anomaly {
	return filterByStrategyField(anomalies, func(s StrategyRef) bool { return s.BusinessID == businessID })
}

func filterByStrategyField(anomalies []Anomaly, match func(StrategyRef) bool) []Anomaly {
	filtered := make([]Anomaly, 0, len(anomalies))
	for _, anomaly := range anomalies {
		for _, strategy := range anomaly.Strategies {
			if match(strategy) {
				filtered = append(filtered, anomaly)
				break
			}
		}
	}
	return filtered
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

func seriesRange(
	response http.ResponseWriter,
	request *http.Request,
	provider RangeProvider,
	now func() time.Time,
) {
	// The window is chosen from a fixed list rather than sent as a duration: a
	// page that can name its own range can also name one nobody budgeted for.
	requested := request.URL.Query().Get("window")
	window, ok := lookupSeriesWindow(requested)
	if !ok {
		writeJSON(response, http.StatusBadRequest,
			map[string]string{"error": "unknown window " + requested})
		return
	}
	at := now()
	writeJSON(response, http.StatusOK, seriesResponse{
		Window: window.Key, WindowChoice: seriesWindowKeys(),
		StartUnixMs: at.Add(-window.Duration).UnixMilli(), EndUnixMs: at.UnixMilli(),
		StepSeconds: int(window.Step / time.Second),
		Series:      collectSeries(request.Context(), provider, window, at),
	})
}

func seriesWindowKeys() []string {
	keys := make([]string, 0, len(seriesWindows))
	for _, window := range seriesWindows {
		keys = append(keys, window.Key)
	}
	return keys
}
