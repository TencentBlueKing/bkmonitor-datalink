// Tencent is pleased to support the open source community by making
// BlueKing available. Copyright (C) 2017-2025 Tencent. Licensed under the MIT License.

package fleet

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// StrategyDirectory is the strategy directory as the control Leader answers
// it from the catalog it published (controlplane.DirectoryView). Available
// is false on every other process, which hands the request to the Leader.
type StrategyDirectory interface {
	Available() bool
	Page(ctx context.Context, at time.Time, tenant, business, strategy string, offset, limit int) controlplane.StrategyDirectorySnapshot
	ResolveCurrent(ctx context.Context, at time.Time, tenant, business, strategy, group string, expectedRevision ...string) (controlplane.StrategyDirectoryRow, error)
	EffectivePlan(ctx context.Context, row controlplane.StrategyDirectoryRow) (controlplane.QueryGroupPlanObject, error)
	EffectiveOutput(ctx context.Context, row controlplane.StrategyDirectoryRow) controlplane.OutputFormatFacts
}

// toLeader hands a directory request this process cannot answer to the
// Leader. It reports whether the response is written: forwarded, or refused
// because there is no Leader to forward to. A request already forwarded is
// answered here, as not ready, and never handed on again.
func toLeader(w http.ResponseWriter, r *http.Request, directory StrategyDirectory, forward LeaderForward) bool {
	if directory.Available() || forward == nil || r.Header.Get(forwardedHeader) != "" {
		return false
	}
	forwarded, refusal := forward(w, r)
	if forwarded {
		return true
	}
	if refusal != "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "LEADER_UNAVAILABLE", "reason": refusal})
		return true
	}
	return false
}

// WithStrategyDirectory extends the existing object endpoint without changing
// its default anomaly-list meaning. The wrapper needs no second HTTP server.
// Only the control Leader answers; a follower forwards the request to it. A
// process that holds no catalog answers not ready with why (absence), read
// from the control plane now.
//
// One page is built at a time: a page's reads go through the runtime's own
// connections, and a second request while one is in flight is refused as
// busy rather than queued behind it.
func WithStrategyDirectory(next http.Handler, directory StrategyDirectory, forward LeaderForward, absence CatalogAbsenceFunc,
	replica string, now func() time.Time) http.Handler {
	pageRead := make(chan struct{}, 1)
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
		if toLeader(w, r, directory, forward) {
			return
		}
		select {
		case pageRead <- struct{}{}:
			defer func() { <-pageRead }()
		default:
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "OBSERVATION_BUSY"})
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
		snapshot := directory.Page(r.Context(), now(), tenant, business, strategy, offset, limit+1)
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
			// NotReady is why this process holds no catalog to answer from,
			// on a not-ready answer: not the Leader, a round still to come or
			// failing, in the words a strategy's standing refuses with.
			NotReady *CatalogAbsence `json:"not_ready,omitempty"`
		}{StrategyDirectorySnapshot: snapshot, NextCursor: nextCursor}
		if snapshot.Reason == "LEADER_CATALOG_NOT_READY" {
			facts := CatalogAbsenceFacts{}
			if absence != nil {
				facts = absence()
			}
			notReady := catalogAbsenceOf(facts, replica, absence != nil)
			body.NotReady = &notReady
		}
		if include := q.Get("include"); include != "" {
			if include != "effective_config" || strategy == "" || offset != 0 {
				writeJSON(w, 400, map[string]string{"error": "INVALID_CONFIG_REQUEST"})
				return
			}
			selected, resolveErr := directory.ResolveCurrent(r.Context(), now(), tenant, business, strategy, q.Get("query_group"), snapshot.Revision)
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
				writeJSON(w, 503, map[string]string{"error": "CONFIG_UNAVAILABLE"})
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
