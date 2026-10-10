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
	"strings"
	"testing"
	"time"
)

func TestManagerUpgradesExistingActiveOperationFields(t *testing.T) {
	t.Parallel()
	transport := newManagerTransport()
	router, err := NewBucketRouter("linkd-close-upgrade", BucketConfig{EventBucketDays: 7, AlertHistoryBucketDays: 7, AlertLogBucketDays: 7, ActiveAlertRefreshInterval: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := New(transport, router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(repo, router, ManagerConfig{PrecreatePastBuckets: 1, PrecreateFutureBuckets: 1, MaxBucketsPerEntity: 512})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileSchemaAndActive(t.Context()); err != nil {
		t.Fatal(err)
	}
	index := router.activeAlertIndex()
	properties := alertProperties()
	delete(properties, "end_operation")
	delete(properties, "last_shield_operation")
	transport.properties[index] = properties
	for range 2 {
		if err := manager.ReconcileSchemaAndActive(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"end_operation", "last_shield_operation"} {
		mapping, _ := transport.properties[index][name].(map[string]any)
		if mapping["type"] != "object" || mapping["enabled"] != false {
			t.Fatalf("existing active mapping lacks operation field %s: %v", name, mapping)
		}
	}
	if len(transport.mappingUpdates) != 1 || transport.mappingUpdates[0] != index {
		t.Fatalf("upgrade is not additive and idempotent: %v", transport.mappingUpdates)
	}
}

func TestManagerUpgradesExistingHistoryOutsidePrecreateWindow(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"", "foreign_metadata", "field_type_conflict", "invalid_bucket_name", "bucket_limit"} {
		t.Run(failure, func(t *testing.T) {
			now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
			transport := newManagerTransport()
			router, err := newBucketRouter("linkd-upgrade", BucketConfig{
				EventBucketDays: 7, AlertHistoryBucketDays: 7, AlertLogBucketDays: 7,
				ActiveAlertRefreshInterval: 5 * time.Second,
			}, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			repository, err := New(transport, router, DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			manager, err := newManager(repository, router, ManagerConfig{
				PrecreatePastBuckets: 1, PrecreateFutureBuckets: 1, MaxBucketsPerEntity: 512,
			}, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			if err := manager.ReconcileSchemaAndActive(t.Context()); err != nil {
				t.Fatal(err)
			}

			old := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
			index := router.alertHistoryIndex(old)
			if failure == "invalid_bucket_name" {
				index = strings.TrimSuffix(index, "20260907") + "20260908"
			}
			metadata := schemaMetadata{ManagedBy: managedByLinkd, Entity: entityAlertHistory, Role: "history", SchemaVersion: currentSchemaVersion,
				BucketDays: 7, BucketStart: old.Format(time.RFC3339), BucketEnd: old.AddDate(0, 0, 7).Format(time.RFC3339)}
			if failure == "foreign_metadata" {
				metadata.ManagedBy = "another-owner"
			}
			transport.indices[index] = metadata
			transport.aliases[router.alertHistoryReadAlias()] = map[string]bool{index: false}
			transport.aliases[router.alertReadAlias()][index] = false
			transport.aliases[router.alertHistoryWriteAlias(old)] = map[string]bool{index: true}
			properties := alertProperties()
			for name := range alertPolicyProperties() {
				delete(properties, name)
			}
			if failure == "field_type_conflict" {
				properties["revision"] = keywordProperty()
			}
			transport.properties[index] = properties
			if failure == "bucket_limit" {
				manager.config.MaxBucketsPerEntity = 2
				transport.aliases[router.alertHistoryReadAlias()][router.alertHistoryIndex(old.AddDate(0, 0, 7))] = false
				transport.aliases[router.alertHistoryReadAlias()][router.alertHistoryIndex(old.AddDate(0, 0, 14))] = false
			}

			err = manager.ReconcileBuckets(t.Context())
			if failure != "" {
				if err == nil || len(transport.mappingUpdates) != 0 {
					t.Fatalf("failure %s: error=%v updates=%v", failure, err, transport.mappingUpdates)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for name := range alertPolicyProperties() {
				if _, ok := transport.properties[index][name]; !ok {
					t.Fatalf("old history mapping still misses %s", name)
				}
			}
			if len(transport.mappingUpdates) != 1 || transport.mappingUpdates[0] != index {
				t.Fatalf("mapping updates %v", transport.mappingUpdates)
			}
			if len(transport.indices) != 11 || transport.indices[index] != metadata {
				t.Fatalf("created intermediate buckets or changed old metadata: %v", transport.indices)
			}
			if !transport.aliases[router.alertHistoryWriteAlias(old)][index] {
				t.Fatal("old write alias changed")
			}
			if err := manager.ReconcileBuckets(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(transport.mappingUpdates) != 1 {
				t.Fatalf("repeated update is not idempotent: %v", transport.mappingUpdates)
			}
		})
	}
}
