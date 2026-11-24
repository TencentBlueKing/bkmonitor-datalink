// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	"linkd/internal/policy"
	"linkd/internal/processingerror"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
	"linkd/internal/taskdispatch"
)

func TestControlPlaneQueryRecoveryAppliesPolicyToOriginalEvent(t *testing.T) {
	for _, failedPath := range []string{"/internal/policy-catalog", "/internal/policy-dependencies"} {
		t.Run(failedPath, func(t *testing.T) {
			loader, event, _, state := suppressionFixture(t)
			event.EventEnrichment = domain.EventEnrichment{EnrichStatus: domain.EnrichStatusPending}
			release := clipRelease(t)
			var unavailable atomic.Bool
			unavailable.Store(true)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == failedPath && unavailable.Load() {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
					return
				}
				var value any = []policy.Release{release}
				if r.URL.Path == "/internal/policy-dependencies" {
					var request DependencyRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
						return
					}
					value = dependencyValue(request, time.Now())
				}
				if err := json.NewEncoder(w).Encode(value); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			worker := NewWorkerDependencies(t.Context(), taskdispatch.Client{URL: server.URL})
			defer worker.Close()
			defer worker.Register(taskdispatch.Task{ID: "task", Worker: "worker"}, event.BKTenantID)()
			catalog := policy.NewCatalog(worker)
			loader.Releases, loader.Catalog, loader.Targets = worker, catalog, worker.Dependencies
			repo := memory.New()
			processor, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, nil, config.DefaultSeverityConfig(), lifecycle.SystemClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithPolicySnapshotter(Snapshotter{Catalog: catalog}), lifecycle.WithNewAlertSuppressor(loader))
			if err != nil {
				t.Fatal(err)
			}
			created, err := repo.CreateEvent(t.Context(), event)
			if err != nil {
				t.Fatal(err)
			}
			for range 4 {
				if _, err := processor.ProcessEvent(t.Context(), created.StoredEvent); !errors.Is(err, processingerror.ErrControlPlaneUnavailable) {
					t.Fatal("query failure became a completed verdict", err)
				}
				stored, err := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
				if err != nil || stored.Processing.State != domain.EventProcessStateUnprocessed || len(stored.Event.RelatedAlertIDs) != 0 || stored.Processing.PolicyDecision != nil {
					t.Fatal("query failure persisted a final decision", stored, err)
				}
			}
			if len(state.requests) != 0 {
				t.Fatal("query failure changed suppression state")
			}
			unavailable.Store(false)
			if failedPath == "/internal/policy-dependencies" {
				refreshDependencyNow(t, worker.Dependencies, DependencyRequest{Kind: "scope", TenantID: event.BKTenantID, SpaceCode: "bkcc__2"})
			}
			stored, _ := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
			if _, err := processor.ProcessEvent(t.Context(), stored); err != nil {
				t.Fatal(err)
			}
			stored, err = repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
			if err != nil || stored.Processing.PolicyDecision == nil || stored.Processing.PolicyDecision.Suppression == nil || !stored.Processing.PolicyDecision.Suppression.Evaluations[0].Suppressed || len(stored.Event.RelatedAlertIDs) != 0 || len(state.requests) != 1 {
				t.Fatal("recovery did not suppress original Event", stored, err)
			}
		})
	}
}

func TestPolicyStagesPropagateControlPlaneFailureButSkipMissingRelease(t *testing.T) {
	for _, release := range []policy.Release{clipRelease(t), timeShieldRelease(t), mergeRelease(t)} {
		for _, failure := range []string{"release", "scope", "not_found", "server_error"} {
			t.Run(string(release.Kind)+"/"+failure, func(t *testing.T) {
				reader := &suppressionReleaseReader{release: release}
				targets := shieldTargets{}
				switch failure {
				case "release":
					reader.err = processingerror.ErrControlPlaneUnavailable
				case "scope":
					targets.err = errors.Join(policy.ErrUnavailable, processingerror.ErrControlPlaneUnavailable)
				case "not_found":
					reader.err = policy.ErrNotFound
				case "server_error":
					reader.err = &taskdispatch.ResponseError{StatusCode: http.StatusInternalServerError}
				}
				loader := &Suppressor{Releases: reader, Targets: targets}
				clock := &policyTestClock{at: time.Date(2026, 9, 30, 0, 0, 1, 0, time.UTC)}
				event := shieldEvent("event", clock)
				event, enrichErr := event.WithEnrichment(storetest.Enrichment(event))
				if enrichErr != nil {
					t.Fatal(enrichErr)
				}
				alert := storetest.Alert(event.BKTenantID, "alert", event.EventID, event.Fingerprint, "warning")
				alert.EventSourceID = event.EventSourceID
				snapshot := &store.PolicyContext{EvaluatedAt: clock.at, Releases: []store.PolicyReleaseRef{{Kind: string(release.Kind), ID: release.ID, Version: release.Version, Digest: release.Compiled.Digest}}}
				var err error
				switch release.Kind {
				case policy.Suppression:
					_, err = loader.Check(t.Context(), event, event.Evaluations[0], snapshot, nil)
				case policy.Shield:
					_, err = (&Shielder{Loader: loader}).Check(t.Context(), event, event.Evaluations[0], snapshot, alert, nil)
				case policy.Merge:
					_, err = (&Merger{Loader: loader}).Check(t.Context(), event, event.Evaluations[0], snapshot, alert, nil)
				}
				if failure == "not_found" || failure == "server_error" {
					if err != nil {
						t.Fatal("missing release entered outage wait", err)
					}
					return
				}
				if !errors.Is(err, processingerror.ErrControlPlaneUnavailable) {
					t.Fatal("control plane failure was swallowed", err)
				}
			})
		}
	}
}

func TestControlPlaneHTTPFailureDoesNotKeepEventPending(t *testing.T) {
	for _, path := range []string{"/internal/policy-catalog", "/internal/policy-dependencies"} {
		for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
			t.Run(fmt.Sprintf("%s/%d", path, status), func(t *testing.T) {
				loader, event, _, _ := suppressionFixture(t)
				event.EventEnrichment = domain.EventEnrichment{EnrichStatus: domain.EnrichStatusPending}
				var requests atomic.Int32
				release := clipRelease(t)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.URL.Path == path {
						http.Error(w, "configuration handling failed", status)
						return
					}
					if err := json.NewEncoder(w).Encode([]policy.Release{release}); err != nil {
						t.Error(err)
					}
				}))
				defer server.Close()
				worker := NewWorkerDependencies(t.Context(), taskdispatch.Client{URL: server.URL})
				defer worker.Close()
				defer worker.Register(taskdispatch.Task{ID: "task", Worker: "worker"}, event.BKTenantID)()
				catalog := policy.NewCatalog(worker)
				loader.Releases, loader.Catalog, loader.Targets = worker, catalog, worker.Dependencies
				repo := memory.New()
				processor, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, nil, config.DefaultSeverityConfig(), lifecycle.SystemClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithPolicySnapshotter(Snapshotter{Catalog: catalog}), lifecycle.WithNewAlertSuppressor(loader))
				if err != nil {
					t.Fatal(err)
				}
				created, err := repo.CreateEvent(t.Context(), event)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := processor.ProcessEvent(t.Context(), created.StoredEvent); err != nil {
					t.Fatal("HTTP error entered outage wait", err)
				}
				stored, err := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
				if err != nil || stored.Processing.State == domain.EventProcessStateUnprocessed {
					t.Fatal("configuration error kept Event pending", stored, err)
				}
				before := requests.Load()
				if _, err := processor.ProcessEvent(t.Context(), stored); err != nil || requests.Load() != before {
					t.Fatal("completed Event repeated configuration query", err)
				}
			})
		}
	}
}
