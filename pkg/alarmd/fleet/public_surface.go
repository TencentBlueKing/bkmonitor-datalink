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
	"fmt"
	"net/http"
	"sync"
	"time"
)

// What a restricted public surface serves of this API: a summary of the
// deployment's health without deployment coordinates, and observation
// windows. Everything else moves behind the CLI session, which reads the
// unrestricted handler.

// PublicHealthResponse is the summary. Restricted says the rest was left out
// on purpose, so an empty list here is not read as "nothing to report".
type PublicHealthResponse struct {
	HealthResponse
	Restricted bool `json:"restricted"`
}

// PublicHealth keeps the counts and the verdict and drops everything that
// names a place, a replica, an object or an error text: dependency addresses
// and prefixes, linkd targets, source and stream facts, per-replica rows,
// gaps, degradations (their errors carry addresses) and object lists. It is
// an allowlist -- a field added to HealthResponse later stays out of the
// public summary until someone decides it belongs -- and the list fields the
// page iterates are sent empty rather than null.
func PublicHealth(full HealthResponse) PublicHealthResponse {
	return PublicHealthResponse{Restricted: true, HealthResponse: HealthResponse{
		Health: full.Health, Expected: full.Expected, Covered: full.Covered,
		Determined: full.Determined, Unknown: full.Unknown, Healthy: full.Healthy,
		AnomaliesTotal: full.AnomaliesTotal, DemotedTotal: full.DemotedTotal,
		UndecidableTotal: full.UndecidableTotal, ByDesignTotal: full.ByDesignTotal,
		EmptyEveryRoundTotal: full.EmptyEveryRoundTotal,
		Ours:                 full.Ours, Unattributed: full.Unattributed,
		DemotedDue: full.DemotedDue, DemotedDueOldestSeconds: full.DemotedDueOldestSeconds,
		DemotionEntries: full.DemotionEntries, DemotionExtensions: full.DemotionExtensions,
		DemotionExits: full.DemotionExits, LastDemotionExit: full.LastDemotionExit,
		DemotionRestored: full.DemotionRestored, DemotionHandovers: full.DemotionHandovers,
		DemotionReentries: full.DemotionReentries,
		PublishedVersion:  full.PublishedVersion, ReplicasNotReady: full.ReplicasNotReady,
		DependenciesReplicas: full.DependenciesReplicas,
		Cohorts:              cohortList(full.Cohorts),
		Builds:               []BuildGroup{}, OutputProtocols: []OutputProtocolGroup{},
		Degradations: []Degradation{}, Dependencies: []Endpoint{},
		Gaps: []Gap{}, PerReplica: []ReplicaView{},
	}}
}

// Limits on what the public window route accepts, on top of the store's own
// (MaxOpenWindows, MaxWindowTTL), which bound what windows cost. These bound
// what the route itself can be made to do: how often any caller may write,
// and how much a caller may write into the audit record.
const (
	// PublicWindowWritesPerMinute is how many opens and closes one replica
	// accepts in public per minute, from everyone together. The page opens
	// one window per click; this is room for several people at once and far
	// below what would make the control-plane writes a load.
	PublicWindowWritesPerMinute = 30
	// PublicOpenedByMaxBytes bounds the name stored with a window.
	PublicOpenedByMaxBytes = 128
)

// publicWindow is a window as the public route shows it: which object, who,
// and when. A sample selection is opened and read through the CLI only.
type publicWindow struct {
	QueryGroup string    `json:"query_group"`
	OpenedBy   string    `json:"opened_by"`
	OpenedAt   time.Time `json:"opened_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// NewPublicWindowsHandler serves /api/windows on a restricted public surface:
// the page's read, open and close, with sample windows left to the CLI, the
// writes bounded per minute, and every store failure answered in fixed words
// -- a store error's text can name the store's address. A nil store serves
// nothing, as the unrestricted route does.
func NewPublicWindowsHandler(store *WindowStore, now func() time.Time) http.Handler {
	if store == nil {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	limiter := &minuteBudget{limit: PublicWindowWritesPerMinute}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		if request.URL.RawQuery != "" {
			writeJSON(response, http.StatusBadRequest, map[string]string{"error": "sample windows and other modes are opened through alarmd-cli"})
			return
		}
		var (
			open []Window
			err  error
		)
		switch request.Method {
		case http.MethodGet:
			open, err = store.Load(request.Context(), now())
		case http.MethodPost:
			var body windowRequest
			if decodeErr := json.NewDecoder(http.MaxBytesReader(response, request.Body, 16<<10)).Decode(&body); decodeErr != nil {
				writeJSON(response, http.StatusBadRequest, map[string]string{"error": "body must be a window request"})
				return
			}
			if refusal := publicWindowRefusal(body); refusal != "" {
				writeJSON(response, http.StatusBadRequest, map[string]string{"error": refusal})
				return
			}
			at := now()
			if wait, ok := limiter.take(at); !ok {
				response.Header().Set("Retry-After", fmt.Sprint(int(wait.Seconds())+1))
				writeJSON(response, http.StatusTooManyRequests, map[string]string{"error": "too many window requests on this replica in the last minute"})
				return
			}
			if body.Close {
				open, err = store.Close(request.Context(), body.QueryGroups, at)
			} else {
				open, err = store.Open(request.Context(), body.QueryGroups, body.OpenedBy, time.Duration(body.TTLSeconds)*time.Second, at)
			}
		default:
			writeJSON(response, http.StatusMethodNotAllowed, map[string]string{"error": "GET to read windows, POST to open or close one"})
			return
		}
		var capped windowCapError
		switch {
		case errors.As(err, &capped):
			writeJSON(response, http.StatusBadRequest, map[string]string{"error": capped.Error()})
			return
		case err != nil:
			writeJSON(response, http.StatusServiceUnavailable, map[string]string{"error": "window store unavailable"})
			return
		}
		shown := make([]publicWindow, 0, len(open))
		for _, window := range open {
			shown = append(shown, publicWindow{QueryGroup: window.QueryGroup, OpenedBy: window.OpenedBy, OpenedAt: window.OpenedAt, ExpiresAt: window.ExpiresAt})
		}
		writeJSON(response, http.StatusOK, map[string]any{"windows": shown, "max_open": MaxOpenWindows, "max_ttl_seconds": int(MaxWindowTTL.Seconds())})
	})
}

// publicWindowRefusal checks everything about the request the store would,
// before the store is asked, so every refusal the caller sees is about what
// it asked for and none is a store error's text.
func publicWindowRefusal(body windowRequest) string {
	if len(body.QueryGroups) == 0 {
		return "at least one query group is required"
	}
	if len(body.QueryGroups) > MaxOpenWindows {
		return fmt.Sprintf("at most %d objects may be observed at once", MaxOpenWindows)
	}
	for _, queryGroup := range body.QueryGroups {
		if err := ValidateQueryGroup(queryGroup); err != nil {
			return "every query group must be a SHA256 identity"
		}
	}
	if body.Close {
		return ""
	}
	if body.OpenedBy == "" || len(body.OpenedBy) > PublicOpenedByMaxBytes {
		return fmt.Sprintf("opened_by is required and at most %d bytes, so an open window can be traced to whoever opened it", PublicOpenedByMaxBytes)
	}
	if ttl := time.Duration(body.TTLSeconds) * time.Second; ttl <= 0 || ttl > MaxWindowTTL {
		return fmt.Sprintf("ttl must be positive and at most %s", MaxWindowTTL)
	}
	return ""
}

// minuteBudget admits at most limit writes per wall-clock minute.
type minuteBudget struct {
	mu     sync.Mutex
	limit  int
	minute time.Time
	used   int
}

// take spends one write, or says how long until the next minute when none is
// left.
func (budget *minuteBudget) take(at time.Time) (time.Duration, bool) {
	budget.mu.Lock()
	defer budget.mu.Unlock()
	minute := at.Truncate(time.Minute)
	if !minute.Equal(budget.minute) {
		budget.minute, budget.used = minute, 0
	}
	if budget.used >= budget.limit {
		return minute.Add(time.Minute).Sub(at), false
	}
	budget.used++
	return 0, true
}
