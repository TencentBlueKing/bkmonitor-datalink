// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package telemetry

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type snapshotMetricsRepository struct {
	store.Repository
	calls int
}

func (r *snapshotMetricsRepository) CompareAndSetEventEnrichmentFromSnapshot(ctx context.Context, current store.StoredEvent, result domain.EventEnrichment) (store.StoredEvent, error) {
	r.calls++
	return r.CompareAndSetEventEnrichment(ctx, current.Event.BKTenantID, current.Event.EventID, current.Version, result)
}

func (r *snapshotMetricsRepository) CompareAndSetLifecycleEventResultFromSnapshot(ctx context.Context, current store.StoredEvent, result store.EventResult) (store.StoredEvent, error) {
	r.calls++
	return r.CompareAndSetEventResult(ctx, current.Event.BKTenantID, current.Event.EventID, current.Version, result)
}

func (r *snapshotMetricsRepository) CompareAndSetAlertFromSnapshot(ctx context.Context, current store.StoredAlert, replacement domain.Alert) (store.StoredAlert, error) {
	r.calls++
	return r.CompareAndSetAlert(ctx, current.Alert.BKTenantID, current.Alert.AlertID, current.Version, replacement)
}

func TestSnapshotCapabilitiesForwardAndFallbackWithoutDoubleCounting(t *testing.T) {
	for _, fast := range []bool{false, true} {
		t.Run(map[bool]string{false: "fallback", true: "snapshot"}[fast], func(t *testing.T) {
			runtime, err := Start(t.Context(), config.TelemetryConfig{Metrics: config.TelemetryMetricsConfig{Exporter: config.TelemetryExporterPrometheus, Prometheus: config.TelemetryPrometheusConfig{ListenAddress: "127.0.0.1:0"}}}, RoleLifecycle, "test")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })
			base := memory.New()
			capable := &snapshotMetricsRepository{Repository: base}
			var next store.Repository = base
			if fast {
				next = capable
			}
			observed := runtime.ObserveRepository(next)
			event := storetest.Event("tenant", "event", "fp", "warning")
			created, err := base.CreateEvent(t.Context(), event)
			if err != nil {
				t.Fatal(err)
			}
			snapshots := observed.(store.LifecycleEventSnapshotStore)
			enriched, err := snapshots.CompareAndSetEventEnrichmentFromSnapshot(t.Context(), created.StoredEvent, storetest.Enrichment(event))
			if err != nil {
				t.Fatal(err)
			}
			_, err = snapshots.CompareAndSetLifecycleEventResultFromSnapshot(t.Context(), enriched, store.EventResult{State: domain.EventProcessStateOrphaned, Outcome: "orphaned", ProcessedAt: event.ReceivedAt})
			if err != nil {
				t.Fatal(err)
			}
			alert := storetest.Alert("tenant", "alert", "opening", "fp", "warning")
			saved, err := base.CreateAlert(t.Context(), alert)
			if err != nil {
				t.Fatal(err)
			}
			replacement := saved.Alert.Clone()
			replacement.LatestEventID = "next"
			replacement.LastOccurredAt = replacement.LastOccurredAt.Add(time.Second)
			replacement.UpdateAt = replacement.UpdateAt.Add(time.Second)
			_, err = observed.(store.LifecycleAlertSnapshotStore).CompareAndSetAlertFromSnapshot(t.Context(), saved.StoredAlert, replacement)
			if err != nil {
				t.Fatal(err)
			}
			if fast && capable.calls != 3 {
				t.Fatalf("snapshot capability hidden: %d", capable.calls)
			}
			req, err := http.NewRequestWithContext(t.Context(), "GET", "http://"+runtime.PrometheusListenAddress()+metricsPath, nil)
			if err != nil {
				t.Fatal(err)
			}
			res, err := (&http.Client{Timeout: time.Second}).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			for _, pair := range [][2]string{{"event", "compare_and_set_enrichment"}, {"event", "compare_and_set_result"}, {"alert", "compare_and_set"}} {
				pattern := `(?m)^linkd_store_operations_total\{[^\n]*linkd_object_type="` + pair[0] + `"[^\n]*linkd_operation="` + pair[1] + `"[^\n]*\} 1$`
				if !regexp.MustCompile(pattern).Match(body) {
					t.Fatalf("missing or double counted %v", pair)
				}
			}
		})
	}
}
