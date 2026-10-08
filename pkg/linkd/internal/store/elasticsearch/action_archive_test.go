// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearchstore

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

func TestElasticsearchPendingActionRemainsDiscoverableUntilArchived(t *testing.T) {
	endpoint := os.Getenv(elasticsearchIntegrationURLEnv)
	if endpoint == "" {
		t.Skip("set LINKD_TEST_ELASTICSEARCH_URL")
	}
	baseURL, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	transport := endpointTransport{baseURL: baseURL, client: &http.Client{Timeout: 15 * time.Second}, apiKey: os.Getenv("LINKD_TEST_ELASTICSEARCH_API_KEY")}
	prefix := "linkd-action-archive-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	now := time.Now().Round(0).UTC()
	router, err := newBucketRouter(prefix, BucketConfig{EventBucketDays: 7, AlertHistoryBucketDays: 7, AlertLogBucketDays: 7, MaxFutureSkew: time.Minute, ActiveAlertRefreshInterval: 5 * time.Second}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	repo, err := New(transport, router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := newManager(repo, router, ManagerConfig{PrecreatePastBuckets: 1, PrecreateFutureBuckets: 1, MaxBucketsPerEntity: 512}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = repo.performJSON(ctx, http.MethodDelete, "/"+prefix+"-*", nil, nil, nil)
		for _, spec := range router.SchemaConfig().Templates() {
			_ = repo.performJSON(ctx, http.MethodDelete, "/_index_template/"+spec.Name, nil, nil, nil)
		}
	})
	if err := manager.ReconcileSchemaAndActive(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileBuckets(t.Context()); err != nil {
		t.Fatal(err)
	}

	e := storetest.Event("tenant", "opening", "fp", "warning")
	e.OccurredAt, e.ProducedAt, e.ReceivedAt, e.CreateAt = now, now, now, now
	e.EventID, err = domain.GenerateEventID(e.BKTenantID, e.EventSourceID, e.SourceEventID, now)
	if err != nil {
		t.Fatal(err)
	}
	id, err := domain.GenerateAlertID(e, now)
	if err != nil {
		t.Fatal(err)
	}
	a := storetest.Alert(e.BKTenantID, id, e.EventID, e.Fingerprint, "warning")
	a.BeginAt, a.CreateAt, a.LastOccurredAt, a.UpdateAt = now, now, now, now.Add(time.Second)
	at := a.UpdateAt
	a.Admission = domain.AlertAdmission{AdmittedAt: &at, Severity: a.Severity, CauseType: "source_event", CauseID: a.LatestEventID}
	a.Projection.Targets = map[string]domain.ProjectionTargetState{"kac": {SourceVersion: 1, RequiredRevision: 1, ActionEnabled: true}}
	a.ActionPending, err = domain.NewAlertActionIntent(a, "source_event", a.LatestEventID)
	if err != nil {
		t.Fatal(err)
	}
	created, err := repo.CreateAlert(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	cleared := created.Alert.Clone()
	cleared.ActionPending = nil
	done, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, created.Version, cleared)
	if err != nil {
		t.Fatal(err)
	}
	next := done.Alert.Clone()
	next.Status = domain.AlertStatusClosed
	next.UpdateAt = next.UpdateAt.Add(time.Second)
	next.EndAt = &next.UpdateAt
	next.EndType = domain.AlertEndTypeUser
	next.EndReason = "manual"
	next.Revision++
	next.Projection, err = next.Projection.RequireRevision(next.Revision)
	if err != nil {
		t.Fatal(err)
	}
	next.ActionPending, err = domain.NewAlertActionIntent(next, "user_operation", "close")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, done.Version, next)
	if err != nil {
		t.Fatal(err)
	}
	refresh := func() {
		t.Helper()
		if err := repo.performJSON(t.Context(), http.MethodPost, "/"+router.activeAlertAlias()+"/_refresh", nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	refresh()
	archived, err := manager.ArchiveTerminalAlerts(t.Context(), ArchiveBatchRequest{Limit: 16, WorkerCount: 1})
	if err != nil || archived.Scanned != 0 || archived.Archived != 0 {
		t.Fatal("pending action archived", archived, err)
	}
	if _, err := repo.prepareArchiveItem(t.Context(), saved); err == nil {
		t.Fatal("direct archive bypassed pending action")
	}
	page, err := repo.ListActionWork(t.Context(), store.ActionWorkCursor{}, 16)
	if err != nil || len(page.Items) != 1 || page.Items[0].Alert.AlertID != id {
		t.Fatal("terminal action invisible", err)
	}
	completed := saved.Alert.Clone()
	completed.ActionPending = nil
	if _, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, id, saved.Version, completed); err != nil {
		t.Fatal(err)
	}
	refresh()
	archived, err = manager.ArchiveTerminalAlerts(t.Context(), ArchiveBatchRequest{Limit: 16, WorkerCount: 1})
	if err != nil || archived.Archived != 1 || archived.Failed != 0 {
		t.Fatal("enqueued action did not allow archive", archived, err)
	}
	final, err := repo.GetAlertCurrent(t.Context(), a.BKTenantID, id)
	if err != nil || final.Alert.ActionPending != nil || final.Alert.Revision != 2 || !final.Alert.Projection.Pending() {
		t.Fatal("archive lost business state or required projection", err)
	}
}
