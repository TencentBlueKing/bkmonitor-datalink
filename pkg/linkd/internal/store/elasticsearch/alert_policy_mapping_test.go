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
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

func TestElasticsearchAddsPolicyFieldsToExistingAlertWithoutRewritingRows(t *testing.T) {
	endpoint := os.Getenv(elasticsearchIntegrationURLEnv)
	if endpoint == "" {
		t.Skip("set LINKD_TEST_ELASTICSEARCH_URL")
	}
	baseURL, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	transport := endpointTransport{baseURL: baseURL, client: &http.Client{Timeout: 15 * time.Second}, apiKey: os.Getenv("LINKD_TEST_ELASTICSEARCH_API_KEY")}
	prefix := "linkd-policy-upgrade-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	router, err := NewStaticRouter(prefix)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := New(transport, router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, nested := range []bool{false, true} {
		t.Run(strconv.FormatBool(nested), func(t *testing.T) {
			index := prefix + "-" + strconv.FormatBool(nested) + "-alerts"
			properties := alertProperties()
			for key := range alertPolicyProperties() {
				delete(properties, key)
			}
			if nested {
				old := alertPolicyProperties()["merge_change"]
				children := old["properties"].(map[string]any)
				delete(children, "kind")
				delete(children, "relation_id")
				properties["merge_change"] = old
			}
			body, err := json.Marshal(map[string]any{"settings": map[string]any{"number_of_shards": 1, "number_of_replicas": 0}, "mappings": map[string]any{"dynamic": "strict", "_meta": schemaMetadata{ManagedBy: managedByLinkd, Entity: entityAlert, SchemaVersion: currentSchemaVersion}, "properties": properties}})
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.performJSON(t.Context(), http.MethodPut, "/"+index, nil, body, nil); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := repo.performJSON(ctx, http.MethodDelete, "/"+index, nil, nil, nil); err != nil {
					t.Error(err)
				}
			})
			encoded, err := json.Marshal(storetest.Alert("tenant", "legacy", "event", "fp", "warning"))
			if err != nil {
				t.Fatal(err)
			}
			var row map[string]any
			if err := json.Unmarshal(encoded, &row); err != nil {
				t.Fatal(err)
			}
			for key := range alertPolicyProperties() {
				delete(row, key)
			}
			encoded, err = json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.performJSON(t.Context(), http.MethodPut, "/"+index+"/_doc/legacy", nil, encoded, nil); err != nil {
				t.Fatal(err)
			}
			if err := repo.EnsureIndex(t.Context(), index, entityAlert); err != nil {
				t.Fatal(err)
			}
			if err := repo.EnsureIndex(t.Context(), index, entityAlert); err != nil {
				t.Fatal("repeated additive upgrade failed", err)
			}
			var found struct {
				Source map[string]any `json:"_source"`
			}
			if err := repo.performJSON(t.Context(), http.MethodGet, "/"+index+"/_doc/legacy", nil, nil, &found); err != nil || !reflect.DeepEqual(found.Source, row) {
				t.Fatal("mapping upgrade rewrote old alert", err)
			}
			var mapping map[string]struct {
				Mappings struct {
					Properties map[string]map[string]any `json:"properties"`
				} `json:"mappings"`
			}
			if err := repo.performJSON(t.Context(), http.MethodGet, "/"+index+"/_mapping", nil, nil, &mapping); err != nil {
				t.Fatal(err)
			}
			for key, want := range alertPolicyProperties() {
				if !mappingContains(mapping[index].Mappings.Properties[key], want) {
					t.Fatalf("policy mapping missing %s", key)
				}
			}
		})
	}

}

func TestElasticsearchPendingMergeRecoveryRemainsDiscoverableUntilArchived(t *testing.T) {
	endpoint := os.Getenv(elasticsearchIntegrationURLEnv)
	if endpoint == "" {
		t.Skip("set LINKD_TEST_ELASTICSEARCH_URL")
	}
	baseURL, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	transport := endpointTransport{baseURL: baseURL, client: &http.Client{Timeout: 15 * time.Second}, apiKey: os.Getenv("LINKD_TEST_ELASTICSEARCH_API_KEY")}
	prefix := "linkd-merge-archive-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
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
	e.EventSourceID = domain.BuiltinMergeEventSourceID
	e.MergeOrigin = &domain.MergeOrigin{OperationID: strings.Repeat("a", 64), WindowID: strings.Repeat("b", 64), MembersDigest: strings.Repeat("c", 64), MemberCount: 2, Policy: domain.PolicyVersion{ID: "merge", Version: 1, Digest: strings.Repeat("d", 64)}}
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
	a.EventSourceID = e.EventSourceID
	a.BeginAt, a.CreateAt, a.LastOccurredAt, a.UpdateAt = now, now, now, now.Add(time.Second)
	relation := strings.Repeat("a", 64)
	a.Merge = &domain.AlertMerge{Role: "aggregate", State: "none", OperationID: relation}
	created, err := repo.CreateAlert(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	next := a.Clone()
	next.UpdateAt = next.UpdateAt.Add(time.Second)
	next.Status = domain.AlertStatusRecovered
	next.EndAt = &next.UpdateAt
	next.EndType = domain.AlertEndTypeSystem
	next.EndReason = "merge_members_ended"
	next.MergeChange = &domain.AlertMergeChange{Kind: "parent_recover", OperationID: "recover", RelationID: relation, WindowID: strings.Repeat("b", 64), EffectiveAt: next.UpdateAt, Before: a.Merge.Clone(), After: next.Merge.Clone()}
	saved, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, created.Version, next)
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
		t.Fatal("pending intent archived", archived, err)
	}
	page, err := repo.ListMergeWork(t.Context(), store.MergeWorkCursor{}, 16)
	if err != nil || len(page.Items) != 1 || page.Items[0].Alert.Alert.AlertID != a.AlertID {
		t.Fatal("pending recovery invisible", err)
	}
	done := saved.Alert.Clone()
	done.MergeChange = nil
	if _, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, saved.Version, done); err != nil {
		t.Fatal(err)
	}
	refresh()
	archived, err = manager.ArchiveTerminalAlerts(t.Context(), ArchiveBatchRequest{Limit: 16, WorkerCount: 1})
	if err != nil || archived.Archived != 1 || archived.Failed != 0 {
		t.Fatal("completed recovery did not archive", archived, err)
	}
}
