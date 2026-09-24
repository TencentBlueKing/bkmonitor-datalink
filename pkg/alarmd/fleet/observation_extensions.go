package fleet

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

type CostCandidatesCache struct {
	body     atomic.Pointer[costCandidatesBody]
	now      func() time.Time
	freshFor time.Duration
}

type costCandidatesBody struct {
	payload []byte
	at      time.Time
}

func NewCostCandidatesCache(now func() time.Time, freshFor time.Duration) *CostCandidatesCache {
	return &CostCandidatesCache{now: now, freshFor: freshFor}
}

// Update is called by the existing fleet maintenance tick. HTTP readers never
// load or decode replica snapshots and never contend with the execution observer.
type CostCandidatesSnapshot struct {
	ObservedAt             time.Time                             `json:"observed_at"`
	Projection             CostProjectionView                    `json:"projection"`
	Registry               ownership.ObservationRegistrySnapshot `json:"registry"`
	Limits                 CostProjectionLimits                  `json:"projection_limits"`
	LocalPublicationFailed bool                                  `json:"local_publication_failed"`
	LocalPublishedBytes    int                                   `json:"local_published_bytes"`
	Scope                  string                                `json:"scope"`
}

func (c *CostCandidatesCache) Update(snapshot CostCandidatesSnapshot) {
	snapshot.Scope = "per_process_observed_candidates; nested wall scopes are not additive; shared group work is not charged to every member"
	payload, err := json.Marshal(snapshot)
	if err == nil {
		c.body.Store(&costCandidatesBody{payload: payload, at: snapshot.ObservedAt})
	}
}

func WithCostCandidates(next http.Handler, cache *CostCandidatesCache) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/objects" || r.URL.Query().Get("scope") != "cost" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		if cache == nil {
			writeJSON(w, 503, map[string]string{"error": "COST_NOT_OBSERVED"})
			return
		}
		body := cache.body.Load()
		if body == nil {
			writeJSON(w, 503, map[string]string{"error": "COST_NOT_OBSERVED"})
			return
		}
		if cache.now().Sub(body.at) > cache.freshFor {
			writeJSON(w, 503, map[string]any{"error": "COST_SNAPSHOT_STALE", "last_observation": json.RawMessage(body.payload)})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write(body.payload)
	})
}

type sampleWindowRequest struct {
	Mode         string `json:"mode"`
	Tenant       string `json:"tenant"`
	Business     string `json:"business"`
	Strategy     string `json:"strategy"`
	QueryGroup   string `json:"query_group"`
	OpenedBy     string `json:"opened_by"`
	TTLSeconds   int    `json:"ttl_seconds"`
	SeriesDigest string `json:"series_digest"`
	SeriesKind   string `json:"series_kind"`
	LevelID      uint32 `json:"level_id"`
}

// WithSeriesSamples reuses the existing window and object routes. Reading a
// sample is explicit; normal lists/refreshes never read sample Redis lists.
func WithSeriesSamples(next http.Handler, directory *controlplane.ObservationDirectory, windows *WindowStore, store *DiagnosticStore, sampler *observability.SeriesSampler, now func() time.Time) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/windows" && r.Method == http.MethodGet && r.URL.Query().Get("mode") == "sample" {
			writeJSON(w, 200, map[string]any{"enabled": directory != nil && windows != nil && store != nil && sampler != nil, "budget": sampleBudget(sampler), "max_window_ttl_seconds": int(MaxWindowTTL.Seconds())})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/objects/") && r.URL.Query().Get("samples") != "" {
			for _, key := range []string{"records", "check", "group"} {
				if r.URL.Query().Has(key) {
					writeJSON(w, 400, map[string]string{"error": "SAMPLE_MODE_DOES_NOT_ACCEPT_DETAIL_PARAMETERS"})
					return
				}
			}
			if r.Method != http.MethodGet {
				w.WriteHeader(405)
				return
			}
			if store == nil || sampler == nil {
				writeJSON(w, 503, map[string]string{"error": "SAMPLE_STORAGE_OR_RESOURCE_BUDGET_UNAVAILABLE"})
				return
			}
			n, err := strconv.Atoi(r.URL.Query().Get("samples"))
			if err != nil || n < 1 || n > DiagnosticRecordsPerObject {
				writeJSON(w, 400, map[string]string{"error": "INVALID_SAMPLE_LIMIT"})
				return
			}
			qg := strings.TrimPrefix(r.URL.Path, "/api/objects/")
			if ValidateQueryGroup(qg) != nil {
				writeJSON(w, 400, map[string]string{"error": "INVALID_QUERY_GROUP"})
				return
			}
			records, err := store.LoadSeriesSamples(r.Context(), qg, n)
			if err != nil {
				writeJSON(w, 503, map[string]string{"error": "SAMPLE_READ_UNAVAILABLE"})
				return
			}
			writeJSON(w, 200, map[string]any{"query_group": qg, "samples": records, "health": sampler.Health(), "store": store.Health(), "budget": sampleBudget(sampler),
				"health_scope": "request_serving_process_cumulative", "record_scope": "retained_window_samples; not complete verdict history"})
			return
		}
		if r.URL.Path != "/api/windows" || r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": "INVALID_WINDOW_BODY"})
			return
		}
		var body sampleWindowRequest
		if err = json.Unmarshal(payload, &body); err != nil {
			writeJSON(w, 400, map[string]string{"error": "INVALID_WINDOW_BODY"})
			return
		}
		if body.Mode == "" {
			r.Body = io.NopCloser(bytes.NewReader(payload))
			next.ServeHTTP(w, r)
			return
		}
		if body.Mode != "sample" || body.Strategy == "" {
			writeJSON(w, 400, map[string]string{"error": "INVALID_SAMPLE_WINDOW"})
			return
		}
		if directory == nil || windows == nil || sampler == nil || store == nil {
			writeJSON(w, 503, map[string]string{"error": "SAMPLE_STORAGE_OR_RESOURCE_BUDGET_UNAVAILABLE"})
			return
		}
		row, err := directory.ResolveCurrent(now(), body.Tenant, body.Business, body.Strategy, body.QueryGroup)
		if err != nil {
			status := 503
			code := "STRATEGY_SELECTION_UNKNOWN"
			if errors.Is(err, controlplane.ErrObservationAmbiguous) {
				status = 409
				code = "AMBIGUOUS_IDENTITY"
			}
			writeJSON(w, status, map[string]string{"error": code})
			return
		}
		selected := row.Activation.Selected
		open, err := windows.OpenSample(r.Context(), string(row.QueryGroup), observability.SeriesSampleSelection{
			TenantID: row.Identity.TenantID, BusinessID: row.Identity.BusinessID, StrategyID: row.Identity.StrategyID,
			StateGeneration: string(selected.StateGeneration), PlanScheduleRevision: string(selected.ScheduleRevision),
			SeriesDigest: body.SeriesDigest, SeriesKind: body.SeriesKind, LevelID: body.LevelID,
		}, body.OpenedBy, time.Duration(body.TTLSeconds)*time.Second, now())
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": "SAMPLE_WINDOW_REJECTED"})
			return
		}
		writeJSON(w, 200, map[string]any{"windows": open, "selection_revision": row.Publication, "budget": sampleBudget(sampler), "coverage": "one pinned series; at most two levels; not a full strategy verdict"})
	})
}

func sampleBudget(sampler *observability.SeriesSampler) map[string]any {
	limits := observability.SeriesSampleLimits{}
	if sampler != nil {
		limits = sampler.Limits()
	}
	return map[string]any{"scope": "request_serving_process; deployment ceiling is the sum across active replicas", "sample_extra_allocation": limits,
		"lifecycle_records_per_minute": observability.TargetFlowMaxRecords, "lifecycle_bytes_per_minute": observability.TargetFlowMaxBytes,
		"combined_records_per_minute": observability.TargetFlowMaxRecords + limits.RecordsPerMinute, "combined_bytes_per_minute": observability.TargetFlowMaxBytes + limits.BytesPerMinute,
		"sample_buffer_reservation_bytes": limits.QueueCapacity * observability.SeriesSampleBufferBytes()}
}
