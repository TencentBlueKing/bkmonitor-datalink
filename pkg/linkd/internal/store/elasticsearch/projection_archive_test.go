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
	"errors"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

func TestElasticsearchProjectionWorkSurvivesArchiveAndConcurrentACK(t *testing.T) {
	endpoint := os.Getenv(elasticsearchIntegrationURLEnv)
	if endpoint == "" {
		t.Skip("set LINKD_TEST_ELASTICSEARCH_URL")
	}
	baseURL, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	transport := endpointTransport{baseURL: baseURL, client: &http.Client{Timeout: 15 * time.Second}, apiKey: os.Getenv("LINKD_TEST_ELASTICSEARCH_API_KEY")}
	prefix := "linkd-projection-archive-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	now := time.Now().UTC()
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
		if err := repo.performJSON(ctx, http.MethodDelete, "/"+prefix+"-*", nil, nil, nil); err != nil {
			t.Error(err)
		}
		for _, spec := range router.SchemaConfig().Templates() {
			if err := repo.performJSON(ctx, http.MethodDelete, "/_index_template/"+spec.Name, nil, nil, nil); err != nil {
				t.Error(err)
			}
		}
	})
	if err := manager.ReconcileSchemaAndActive(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileBuckets(t.Context()); err != nil {
		t.Fatal(err)
	}

	refresh := func(t *testing.T) {
		t.Helper()
		if err := repo.performJSON(t.Context(), http.MethodPost, "/"+router.alertReadAlias()+"/_refresh", nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	acknowledge := func(t *testing.T, row store.StoredAlert, target string) store.StoredAlert {
		t.Helper()
		next := row.Alert.Clone()
		next.Projection, _, err = next.Projection.Acknowledge(target, 1, next.Revision, now)
		if err != nil {
			t.Fatal(err)
		}
		saved, err := repo.CompareAndSetAlert(t.Context(), next.BKTenantID, next.AlertID, row.Version, next)
		if err != nil {
			t.Fatal(err)
		}
		if saved.Alert.Revision != row.Alert.Revision || !saved.Alert.UpdateAt.Equal(row.Alert.UpdateAt) {
			t.Fatal("ACK advanced business state")
		}
		return saved
	}
	for _, interleaved := range []bool{false, true} {
		t.Run(strconv.FormatBool(interleaved), func(t *testing.T) {
			// 前一历史桶的数据也必须进入全量补扫，不能因终态或最近时间过滤漏掉。
			opening := now.Add(-7 * 24 * time.Hour)
			e := storetest.Event("tenant", "opening-"+strconv.FormatBool(interleaved), "fp-"+strconv.FormatBool(interleaved), "warning")
			e.OccurredAt, e.ProducedAt, e.ReceivedAt, e.CreateAt = opening, opening, opening, opening
			e.EventID, err = domain.GenerateEventID(e.BKTenantID, e.EventSourceID, e.SourceEventID, opening)
			if err != nil {
				t.Fatal(err)
			}
			id, err := domain.GenerateAlertID(e, opening)
			if err != nil {
				t.Fatal(err)
			}
			a := storetest.Alert(e.BKTenantID, id, e.EventID, e.Fingerprint, "warning")
			a.BeginAt, a.CreateAt, a.LastOccurredAt, a.UpdateAt = opening, opening, opening, opening
			a.Projection = domain.AlertProjection{Targets: map[string]domain.ProjectionTargetState{"kac": {SourceVersion: 1, RequiredRevision: 1}, "audit": {SourceVersion: 1, RequiredRevision: 1}}}
			created, err := repo.CreateAlert(t.Context(), a)
			if err != nil {
				t.Fatal(err)
			}
			next := created.Alert.Clone()
			next.UpdateAt = now
			next.Status, next.EndType, next.EndReason = domain.AlertStatusRecovered, domain.AlertEndTypeSource, "resolved"
			next.EndAt = &next.UpdateAt
			terminal, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, created.Version, next)
			if err != nil {
				t.Fatal(err)
			}
			if interleaved {
				// 模拟 History 已创建，Active 尚未条件删除；两个确认分别落到两个真实副本。
				route, err := router.AlertHistoryRoute(t.Context(), a.AlertID)
				if err != nil {
					t.Fatal(err)
				}
				body, err := encodeAlertDocument(terminal.Alert)
				if err != nil {
					t.Fatal(err)
				}
				if err := repo.performJSON(t.Context(), http.MethodPut, "/"+route.WriteTarget+"/_create/"+alertDocumentID(a), url.Values{"require_alias": {"true"}, "refresh": {"wait_for"}}, body, nil); err != nil {
					t.Fatal(err)
				}
				history, err := repo.getAlertFromTarget(t.Context(), route.WriteTarget, a.BKTenantID, a.AlertID, alertDocumentID(a))
				if err != nil {
					t.Fatal(err)
				}
				acknowledge(t, history, "audit")
				terminal = acknowledge(t, terminal, "kac")
			}
			refresh(t)
			result, err := manager.ArchiveTerminalAlerts(t.Context(), ArchiveBatchRequest{Limit: 16, WorkerCount: 1})
			if err != nil || result.Archived != 1 || result.Failed != 0 {
				t.Fatal("archive did not converge", result, err)
			}
			refresh(t)
			current, err := repo.GetAlertCurrent(t.Context(), a.BKTenantID, a.AlertID)
			if err != nil {
				t.Fatal(err)
			}
			if current.Alert.Revision != terminal.Alert.Revision || !current.Alert.UpdateAt.Equal(terminal.Alert.UpdateAt) {
				t.Fatal("archive altered business snapshot")
			}
			if _, err := repo.getAlertFromTarget(t.Context(), router.activeAlertAlias(), a.BKTenantID, a.AlertID, alertDocumentID(a)); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("active copy retained", err)
			}
			page, err := repo.ListProjectionWork(t.Context(), store.ProjectionWorkCursor{}, 16)
			if err != nil {
				t.Fatal(err)
			}
			if interleaved {
				if len(page.Items) != 0 || current.Alert.Projection.Pending() {
					t.Fatal("independent ACKs were lost")
				}
			} else {
				if len(page.Items) != 2 {
					t.Fatalf("history pending targets invisible: %d", len(page.Items))
				}
				for _, item := range page.Items {
					if item.Alert.Alert.AlertID != a.AlertID {
						t.Fatal("wrong pending alert")
					}
					current = acknowledge(t, current, item.TargetID)
				}
				refresh(t)
				page, err = repo.ListProjectionWork(t.Context(), store.ProjectionWorkCursor{}, 16)
				if err != nil || len(page.Items) != 0 {
					t.Fatal("confirmed history remains pending", err)
				}
			}
		})
	}
}

func TestCollapseAlertHitsPreservesRealVersionAndRejectsConflicts(t *testing.T) {
	a := storetest.Alert("tenant", "alert", "opening", "fp", "warning")
	a.Projection = domain.AlertProjection{Targets: map[string]domain.ProjectionTargetState{"kac": {SourceVersion: 1, RequiredRevision: 1}, "audit": {SourceVersion: 1, RequiredRevision: 1}}}
	hit := func(index string, a domain.Alert) searchHit {
		t.Helper()
		body, err := encodeAlertDocument(a)
		if err != nil {
			t.Fatal(err)
		}
		return searchHit{Index: index, ID: alertDocumentID(a), SeqNo: 1, PrimaryTerm: 1, Source: body}
	}
	ack := func(a domain.Alert, id string) domain.Alert {
		t.Helper()
		a = a.Clone()
		var err error
		a.Projection, _, err = a.Projection.Acknowledge(id, 1, 1, a.UpdateAt)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	b := ack(a, "kac")
	bh := hit("history", b)
	expected, err := decodeAlertHit(bh)
	if err != nil {
		t.Fatal(err)
	}
	for _, hits := range [][]searchHit{{hit("active", a), bh}, {bh, hit("active", a)}} {
		got, err := collapseAlertHits(hits, a.BKTenantID, a.AlertID)
		if err != nil || !reflect.DeepEqual(got, expected) {
			t.Fatal("did not select actual dominating copy", err)
		}
	}
	for name, other := range map[string]domain.Alert{
		"incomparable confirmations": ack(a, "audit"),
		"different business":         func() domain.Alert { c := b.Clone(); c.Title = "different"; return c }(),
		"different binding": func() domain.Alert {
			c := b.Clone()
			v := c.Projection.Targets["kac"]
			v.SourceVersion = 2
			c.Projection.Targets["kac"] = v
			return c
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := collapseAlertHits([]searchHit{bh, hit("active", other)}, a.BKTenantID, a.AlertID); !errors.Is(err, store.ErrIdentityConflict) {
				t.Fatal("conflicting copies accepted", err)
			}
		})
	}
}
