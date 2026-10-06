// Tencent is pleased to support the open source community by making
// BlueKing available. Copyright (C) 2017-2025 Tencent. Licensed under the MIT License.

package fleet

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// WithStrategyDirectory extends the existing object endpoint without changing
// its default anomaly-list meaning. The wrapper needs no second HTTP server.
func WithStrategyDirectory(next http.Handler, directory *controlplane.ObservationDirectory, now func() time.Time) http.Handler {
	// Configuration reads are point reads but can decode a large shared group.
	// Only one may run per process; the diagnostic request is rejected instead
	// of occupying more execution connections or keeping a waiting queue.
	configRead := make(chan struct{}, 1)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/api/objects" || q.Get("scope") != "strategies" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if directory == nil {
			writeJSON(w, 503, map[string]string{"error": "DIRECTORY_RESOURCE_OR_STORE_UNAVAILABLE"})
			return
		}
		limit := 20
		if raw := q.Get("limit"); raw != "" {
			n, e := strconv.Atoi(raw)
			if e != nil || n < 1 || n > 200 {
				writeJSON(w, 400, map[string]string{"error": "INVALID_LIMIT"})
				return
			}
			limit = n
		}
		tenant, business, strategy := q.Get("tenant"), q.Get("business"), q.Get("strategy")
		offset := 0
		cursorRevision := ""
		if cursor := q.Get("cursor"); cursor != "" {
			b, e := base64.RawURLEncoding.DecodeString(cursor)
			parts := strings.Split(string(b), "\n")
			if e != nil || len(parts) != 5 || parts[2] != tenant || parts[3] != business || parts[4] != strategy {
				writeJSON(w, 400, map[string]string{"error": "INVALID_CURSOR"})
				return
			}
			cursorRevision = parts[0]
			offset, e = strconv.Atoi(parts[1])
			if e != nil || offset < 0 {
				writeJSON(w, 400, map[string]string{"error": "INVALID_CURSOR"})
				return
			}
		}
		snapshot := directory.Page(now(), tenant, business, strategy, offset, limit+1)
		if len(snapshot.Unattributed) > limit {
			snapshot.Unattributed = snapshot.Unattributed[:limit]
			snapshot.SourceTruncated = true
		}
		if cursorRevision != "" && (cursorRevision != snapshot.Revision || !snapshot.Complete) {
			writeJSON(w, 409, map[string]string{"error": "CURSOR_STALE"})
			return
		}
		var nextCursor string
		if len(snapshot.Rows) > limit {
			snapshot.Rows = snapshot.Rows[:limit]
			if snapshot.Complete {
				nextCursor = base64.RawURLEncoding.EncodeToString([]byte(snapshot.Revision + "\n" + strconv.Itoa(offset+limit) + "\n" + tenant + "\n" + business + "\n" + strategy))
			}
		}
		body := struct {
			controlplane.StrategyDirectorySnapshot
			NextCursor      string `json:"next_cursor,omitempty"`
			EffectiveConfig any    `json:"effective_config,omitempty"`
			ConfigEvidence  string `json:"config_evidence,omitempty"`
			// EffectiveOutput is what this Plan publishes as, read off the
			// output context frozen with it. Beside the configuration because
			// the execution object it comes from has no format in it on
			// purpose, and the question "what did the deployment's choice come
			// out as for this strategy" is asked with the configuration.
			EffectiveOutput *controlplane.OutputFormatFacts `json:"effective_output,omitempty"`
		}{StrategyDirectorySnapshot: snapshot, NextCursor: nextCursor}
		if include := q.Get("include"); include != "" {
			if include != "effective_config" || strategy == "" || offset != 0 {
				writeJSON(w, 400, map[string]string{"error": "INVALID_CONFIG_REQUEST"})
				return
			}
			selected, resolveErr := directory.ResolveCurrent(now(), tenant, business, strategy, q.Get("query_group"), snapshot.Revision)
			if resolveErr != nil {
				if errors.Is(resolveErr, controlplane.ErrObservationChanged) {
					writeJSON(w, 409, map[string]string{"error": "CURSOR_STALE"})
				} else if errors.Is(resolveErr, controlplane.ErrObservationAmbiguous) {
					writeJSON(w, 409, map[string]string{"error": "AMBIGUOUS_IDENTITY"})
				} else {
					writeJSON(w, 503, map[string]string{"error": "EFFECTIVE_CONFIG_UNKNOWN"})
				}
				return
			}
			select {
			case configRead <- struct{}{}:
				defer func() { <-configRead }()
			default:
				writeJSON(w, 429, map[string]string{"error": "OBSERVATION_BUSY"})
				return
			}
			plan, err := directory.EffectivePlan(r.Context(), selected)
			if err != nil {
				code := "CONFIG_UNAVAILABLE"
				if errors.Is(err, controlplane.ErrObservationBudget) {
					code = "RESOURCE_BUDGET"
				}
				writeJSON(w, 503, map[string]string{"error": code})
				return
			}
			body.EffectiveConfig = plan
			body.ConfigEvidence = "activation_observed; not a historical Slot verdict"
			// The same single flight, the same allowance: one more bounded
			// point read, answered from the process's own cache on the replica
			// that renders this Plan. Not known is an answer here, not a 503:
			// the configuration above was read, and the output half says why
			// it was not.
			output := directory.EffectiveOutput(r.Context(), selected)
			body.EffectiveOutput = &output
		}
		status := http.StatusOK
		// An empty partial observation is UNKNOWN, not an authoritative 404.
		if !snapshot.Complete || strategy != "" && len(snapshot.Rows) == 0 {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, body)
	})
}
