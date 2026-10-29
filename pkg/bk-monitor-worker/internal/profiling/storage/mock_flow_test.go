// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/core"
)

func TestActiveProfilingFullFlow(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "testdata", "active_profiling_events.json"))
	if err != nil {
		t.Fatal(err)
	}
	events, err := core.DecodeMessage(payload)
	if err != nil {
		t.Fatalf("decode mock Events: %s", err)
	}
	if len(events) != 2 {
		t.Fatalf("decoded mock Events = %d, want 2", len(events))
	}

	clock := newTestClock(time.UnixMilli(events[0].ScheduledAt))
	store := NewMockStore()
	processor, err := core.NewProcessor(store, core.Options{
		WindowMaxDuration:    time.Second,
		WindowDelay:          time.Second,
		UnknownScanInterval:  10 * time.Millisecond,
		UnknownScanBatchSize: 10,
		Now:                  clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := processor.HandleMessage(context.Background(), payload); err != nil {
		t.Fatalf("handle mock Event message: %s", err)
	}
	key := core.ExecutionKey{
		TenantID:    events[0].TenantID,
		BizID:       events[0].BizID,
		TaskID:      events[0].TaskID,
		ScheduledAt: events[0].ScheduledAt,
	}
	beforeExpiry, ok := store.Snapshot(key)
	if !ok {
		t.Fatal("mock execution was not persisted")
	}
	assertHostStatus(t, beforeExpiry, 101, "succeed")
	assertHostStatus(t, beforeExpiry, 202, core.StatusRunning)
	if got := beforeExpiry.Execution.ExecutionStatus; got != core.StatusRunning {
		t.Fatalf("execution status before expiry = %q, want running", got)
	}

	// Advance beyond the placeholder window's scheduled_at + duration + delay.
	clock.Advance(3 * time.Second)
	if err := processor.SweepUnknownNow(context.Background()); err != nil {
		t.Fatalf("sweep expired windows: %s", err)
	}

	afterExpiry, ok := store.Snapshot(key)
	if !ok {
		t.Fatal("mock execution disappeared after window expiry")
	}
	assertHostStatus(t, afterExpiry, 101, "succeed")
	assertHostStatus(t, afterExpiry, 202, core.StatusUnknown)
	if got := afterExpiry.Execution.ExecutionStatus; got != "partial_succeed" {
		t.Fatalf("execution status after expiry = %q, want partial_succeed", got)
	}
	if afterExpiry.Execution.TargetCount != 2 {
		t.Fatalf("execution target_count = %d, want 2", afterExpiry.Execution.TargetCount)
	}
	var summary map[string]int
	if err := json.Unmarshal(afterExpiry.Execution.StatusSummary, &summary); err != nil {
		t.Fatalf("decode execution status summary: %s", err)
	}
	if summary["succeed"] != 1 || summary[core.StatusUnknown] != 1 {
		t.Fatalf("execution status summary = %v, want succeed=1 and unknown=1", summary)
	}
}

func assertHostStatus(t *testing.T, snapshot MockSnapshot, hostID int, want string) {
	t.Helper()
	for _, host := range snapshot.Hosts {
		if host.BkHostID == hostID {
			if host.Status != want {
				t.Fatalf("host %d status = %q, want %q", hostID, host.Status, want)
			}
			return
		}
	}
	t.Fatalf("host %d is missing from mock snapshot", hostID)
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock(now time.Time) *testClock {
	return &testClock{now: now}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}
