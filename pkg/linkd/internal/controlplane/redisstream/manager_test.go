// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redisstream

import (
	"context"
	"errors"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
)

type fakeRedis struct {
	exists       int64
	existsErr    error
	info         redis.XInfoStream
	groups       []redis.XInfoGroup
	pending      map[string][]redis.XPendingExt
	memory       int64
	trimmed      int64
	trimBoundary string
	trimLimit    int64
}

func (f *fakeRedis) Exists(context.Context, ...string) *redis.IntCmd {
	return redis.NewIntResult(f.exists, f.existsErr)
}

func (f *fakeRedis) XInfoStream(ctx context.Context, key string) *redis.XInfoStreamCmd {
	command := redis.NewXInfoStreamCmd(ctx, key)
	value := f.info
	command.SetVal(&value)
	return command
}

func (f *fakeRedis) XInfoGroups(ctx context.Context, key string) *redis.XInfoGroupsCmd {
	command := redis.NewXInfoGroupsCmd(ctx, key)
	command.SetVal(append([]redis.XInfoGroup(nil), f.groups...))
	return command
}

func (f *fakeRedis) XPendingExt(ctx context.Context, args *redis.XPendingExtArgs) *redis.XPendingExtCmd {
	command := redis.NewXPendingExtCmd(ctx)
	command.SetVal(append([]redis.XPendingExt(nil), f.pending[args.Group]...))
	return command
}

func (f *fakeRedis) MemoryUsage(context.Context, string, ...int) *redis.IntCmd {
	return redis.NewIntResult(f.memory, nil)
}

func (f *fakeRedis) XTrimMinIDApprox(
	_ context.Context,
	_, minID string,
	limit int64,
) *redis.IntCmd {
	f.trimBoundary = minID
	f.trimLimit = limit
	return redis.NewIntResult(f.trimmed, nil)
}

type recordingObserver struct {
	snapshots []Snapshot
	outcomes  []string
	trimmed   int64
}

func (o *recordingObserver) ObserveSnapshot(_ context.Context, snapshot Snapshot) {
	o.snapshots = append(o.snapshots, snapshot)
}

func (o *recordingObserver) ReconcileFinished(
	_ context.Context,
	outcome string,
	_ time.Duration,
	trimmed int64,
) {
	o.outcomes = append(o.outcomes, outcome)
	o.trimmed += trimmed
}

func TestManagerCollectsStreamAndGroupMetrics(t *testing.T) {
	t.Parallel()
	now := time.UnixMilli(10_000)
	client := &fakeRedis{
		exists: 1,
		info: redis.XInfoStream{
			Length: 80, EntriesAdded: 120, FirstEntry: redis.XMessage{ID: "1000-0"},
		},
		groups: []redis.XInfoGroup{
			{Name: "lifecycle", Consumers: 2, Pending: 1, Lag: 3, LastDeliveredID: "8000-0"},
			{Name: "audit", Consumers: 1, Pending: 1, Lag: 7, LastDeliveredID: "7000-0"},
		},
		pending: map[string][]redis.XPendingExt{
			"lifecycle": {{ID: "5000-0"}},
			"audit":     {{ID: "4000-0"}},
		},
		memory: 4096,
	}
	observer := &recordingObserver{}
	manager := newTestManager(t, client, observer, now)
	if err := manager.ReconcileOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(observer.snapshots) != 1 || len(observer.outcomes) != 1 || observer.outcomes[0] != "succeeded" {
		t.Fatalf("observer=%#v", observer)
	}
	snapshot := observer.snapshots[0]
	if !snapshot.Exists || !snapshot.ExpectedGroupPresent || snapshot.Length != 80 || snapshot.EntriesAdded != 120 ||
		snapshot.MemoryBytes != 4096 || snapshot.Groups != 2 || snapshot.Consumers != 3 || snapshot.Pending != 2 ||
		snapshot.MaxLag != 7 || snapshot.OldestEntryAgeSeconds != 9 || snapshot.OldestPendingAgeSeconds != 6 ||
		snapshot.TrimRequired || !snapshot.TrimSafe {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	if client.trimBoundary != "" {
		t.Fatalf("unexpected trim boundary %q", client.trimBoundary)
	}
}

func TestManagerTrimsOnlyBeforeEveryGroupSafeBoundary(t *testing.T) {
	t.Parallel()
	client := &fakeRedis{
		exists: 1,
		info:   redis.XInfoStream{Length: 101, FirstEntry: redis.XMessage{ID: "1000-0"}},
		groups: []redis.XInfoGroup{
			{Name: "lifecycle", Pending: 1, LastDeliveredID: "9000-0"},
			{Name: "audit", Pending: 0, LastDeliveredID: "8000-4"},
		},
		pending: map[string][]redis.XPendingExt{"lifecycle": {{ID: "7000-2"}}},
		trimmed: 25,
	}
	observer := &recordingObserver{}
	manager := newTestManager(t, client, observer, time.UnixMilli(10_000))
	if err := manager.ReconcileOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.trimBoundary != "7000-2" || client.trimLimit != 25 {
		t.Fatalf("trim boundary=%q limit=%d", client.trimBoundary, client.trimLimit)
	}
	if observer.snapshots[0].EntriesAboveConfiguredMax != 1 || !observer.snapshots[0].TrimRequired ||
		!observer.snapshots[0].TrimSafe {
		t.Fatalf("snapshot=%#v", observer.snapshots[0])
	}
	if observer.trimmed != 25 {
		t.Fatalf("observer trimmed=%d", observer.trimmed)
	}
}

func TestManagerIncludesLastDeliveredEntryWhenAllGroupsAcknowledged(t *testing.T) {
	t.Parallel()
	client := &fakeRedis{
		exists: 1,
		info:   redis.XInfoStream{Length: 101, FirstEntry: redis.XMessage{ID: "1000-0"}},
		groups: []redis.XInfoGroup{{Name: "lifecycle", LastDeliveredID: "9000-4"}},
	}
	manager := newTestManager(t, client, &recordingObserver{}, time.UnixMilli(10_000))
	if err := manager.ReconcileOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.trimBoundary != "9000-5" {
		t.Fatalf("trim boundary=%q", client.trimBoundary)
	}
}

func TestManagerDoesNotTrimWithoutExpectedGroupOrStablePendingSnapshot(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		groups  []redis.XInfoGroup
		pending map[string][]redis.XPendingExt
	}{
		{
			name:   "expected group missing",
			groups: []redis.XInfoGroup{{Name: "other", LastDeliveredID: "9000-0"}},
		},
		{
			name:    "pending acknowledged between commands",
			groups:  []redis.XInfoGroup{{Name: "lifecycle", Pending: 1, LastDeliveredID: "9000-0"}},
			pending: map[string][]redis.XPendingExt{"lifecycle": {}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := &fakeRedis{
				exists:  1,
				info:    redis.XInfoStream{Length: 101, FirstEntry: redis.XMessage{ID: "1000-0"}},
				groups:  test.groups,
				pending: test.pending,
			}
			observer := &recordingObserver{}
			manager := newTestManager(t, client, observer, time.UnixMilli(10_000))
			if err := manager.ReconcileOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			if client.trimBoundary != "" {
				t.Fatalf("unsafe trim boundary %q", client.trimBoundary)
			}
			if len(observer.snapshots) != 1 || !observer.snapshots[0].TrimRequired || observer.snapshots[0].TrimSafe {
				t.Fatalf("snapshot=%#v", observer.snapshots)
			}
		})
	}
}

func TestManagerReportsMissingStreamAndCollectionFailure(t *testing.T) {
	t.Parallel()
	observer := &recordingObserver{}
	manager := newTestManager(t, &fakeRedis{}, observer, time.UnixMilli(10_000))
	if err := manager.ReconcileOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(observer.snapshots) != 1 || observer.snapshots[0].Exists || observer.snapshots[0].MaxEntries != 100 {
		t.Fatalf("snapshots=%#v", observer.snapshots)
	}

	failureObserver := &recordingObserver{}
	failureManager := newTestManager(t, &fakeRedis{existsErr: errors.New("redis unavailable")}, failureObserver, time.Now())
	if err := failureManager.ReconcileOnce(context.Background()); err == nil {
		t.Fatal("ReconcileOnce() error = nil")
	}
	if len(failureObserver.snapshots) != 0 || len(failureObserver.outcomes) != 1 || failureObserver.outcomes[0] != "failed" {
		t.Fatalf("failure observer=%#v", failureObserver)
	}
}

func TestNextStreamID(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"1000-4":                    "1000-5",
		"1000-18446744073709551615": "1001-0",
	}
	for input, expected := range tests {
		actual, err := nextStreamID(input)
		if err != nil || actual != expected {
			t.Fatalf("nextStreamID(%q) = %q, %v; want %q", input, actual, err, expected)
		}
	}
	if _, err := nextStreamID("invalid"); err == nil {
		t.Fatal("nextStreamID(invalid) error = nil")
	}
}

func newTestManager(
	t *testing.T,
	client redisClient,
	observer Observer,
	now time.Time,
) *Manager {
	t.Helper()
	manager, err := newManager(client, Config{
		Stream: "signals", ExpectedGroup: "lifecycle", ReconcileInterval: time.Minute,
		OperationTimeout: time.Second, MaxEntries: 100, TrimBatchSize: 25,
	}, observer, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

var _ redisClient = (*fakeRedis)(nil)

var _ Observer = (*recordingObserver)(nil)
