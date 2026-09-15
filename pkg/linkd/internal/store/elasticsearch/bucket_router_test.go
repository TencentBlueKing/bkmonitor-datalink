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
	"testing"
	"time"

	"linkd/internal/domain"
)

func TestBucketRouterRoutesStructuredIDs(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 2, 16, 0, 0, 0, time.UTC)
	router, err := newBucketRouter("linkd-test", BucketConfig{
		EventBucketDays: 7, AlertHistoryBucketDays: 7, AlertLogBucketDays: 14,
		MaxFutureSkew: 5 * time.Minute,
	}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	eventID, err := domain.GenerateEventID("tenant-1", "source-1", "event-1", now)
	if err != nil {
		t.Fatal(err)
	}
	eventRoute, err := router.EventRoute(context.Background(), eventID)
	if err != nil {
		t.Fatal(err)
	}
	if eventRoute.WriteTarget != "linkd-test-events-write-20260831" || !eventRoute.RequireAlias {
		t.Fatalf("event route=%#v", eventRoute)
	}
	event := eventForIDTest(t, eventID, now)
	alertID, err := domain.GenerateAlertID(event, event.CreateAt)
	if err != nil {
		t.Fatal(err)
	}
	alertRoute, err := router.AlertRoute(context.Background(), alertID)
	if err != nil {
		t.Fatal(err)
	}
	if alertRoute.WriteTarget != "linkd-test-alerts-write" || len(alertRoute.ReadTargets) != 2 ||
		alertRoute.ReadTargets[1] != "linkd-test-alert-history-write-20260831" {
		t.Fatalf("alert route=%#v", alertRoute)
	}
	logRoute, err := router.AlertLogWriteRoute(context.Background(), alertID, "log-1")
	if err != nil {
		t.Fatal(err)
	}
	if logRoute.WriteTarget != "linkd-test-alert-logs-write-20260831" {
		t.Fatalf("alert log route=%#v", logRoute)
	}
}

func TestBucketRouterRejectsFutureEvent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 2, 16, 0, 0, 0, time.UTC)
	router, err := newBucketRouter("linkd-test", BucketConfig{
		EventBucketDays: 7, AlertHistoryBucketDays: 7, AlertLogBucketDays: 7,
		MaxFutureSkew: time.Minute,
	}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	id, err := domain.GenerateEventID("tenant-1", "source-1", "future", now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := router.EventRoute(context.Background(), id); err == nil {
		t.Fatal("future event route accepted")
	}
}

func TestBucketRouterConfiguresReplicaCountForNewIndices(t *testing.T) {
	t.Parallel()
	zero := 0
	router, err := NewBucketRouter("linkd-test", BucketConfig{
		EventBucketDays: 7, AlertHistoryBucketDays: 7, AlertLogBucketDays: 7,
		MaxFutureSkew: 5 * time.Minute, NumberOfReplicas: &zero,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range router.SchemaConfig().Templates() {
		if replicas, exists := spec.Settings["number_of_replicas"]; !exists || replicas != 0 {
			t.Fatalf("template %q settings=%#v", spec.Name, spec.Settings)
		}
	}
}

func TestBucketRouterLeavesReplicaCountUnmanagedWhenOmitted(t *testing.T) {
	t.Parallel()
	router, err := NewBucketRouter("linkd-test", BucketConfig{
		EventBucketDays: 7, AlertHistoryBucketDays: 7, AlertLogBucketDays: 7,
		MaxFutureSkew: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range router.SchemaConfig().Templates() {
		if spec.Settings != nil {
			t.Fatalf("template %q settings=%#v, want nil", spec.Name, spec.Settings)
		}
	}
}

func TestBucketStartUsesUTCStableEpoch(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		value time.Time
		days  int
		want  string
	}{
		{time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC), 7, "20260831"},
		{time.Date(2026, 9, 6, 23, 59, 59, 0, time.FixedZone("east", 8*3600)), 7, "20260831"},
		{time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC), 7, "20260907"},
	} {
		if got := bucketStart(test.value, test.days).Format(bucketDateLayout); got != test.want {
			t.Fatalf("bucketStart(%s,%d)=%s, want %s", test.value, test.days, got, test.want)
		}
	}
}

func eventForIDTest(t *testing.T, eventID string, timestamp time.Time) domain.Event {
	t.Helper()
	return domain.Event{
		BKTenantID: "tenant-1", EventSourceID: "source-1", EventID: eventID,
		Fingerprint: "fp", Severity: "warning", Action: domain.EventActionTriggered,
		Dimensions: domain.DimensionMap{}, Labels: domain.DimensionMap{}, SourceRawData: domain.JSONObject{}, ExtraData: domain.JSONObject{},
		OccurredAt: timestamp, ProducedAt: timestamp, ReceivedAt: timestamp, CreateAt: timestamp,
	}
}
