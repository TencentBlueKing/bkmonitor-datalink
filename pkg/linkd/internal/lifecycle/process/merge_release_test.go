// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycleprocess

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/mailbox"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

func TestMergeOperatorBoundsInflightAndCancelsQueuedWork(t *testing.T) {
	entered := make(chan struct{}, 4)
	done := make(chan struct{})
	var active atomic.Int32
	releaser := &MergeOperator{slots: make(chan struct{}, 4), run: func(ctx context.Context, _, _, _, _ string) (store.StoredAlert, error) {
		n := active.Add(1)
		defer active.Add(-1)
		if n > 4 {
			t.Error("merge release concurrency exceeded")
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 10*time.Second {
			t.Error("missing bounded release deadline")
		}
		entered <- struct{}{}
		select {
		case <-done:
			return store.StoredAlert{}, nil
		case <-ctx.Done():
			return store.StoredAlert{}, ctx.Err()
		}
	}}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if _, err := releaser.ReleaseMergeWindow(t.Context(), "tenant", "alert", "window"); err != nil {
				t.Error(err)
			}
		})
	}
	for range 4 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("release failed to enter")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := releaser.ReleaseMergeWindow(ctx, "tenant", "queued", "window"); !errors.Is(err, context.Canceled) {
		t.Fatal("queue cancellation lost", err)
	}
	close(done)
	wg.Wait()
	if len(releaser.slots) != 0 {
		t.Fatal("release slot leaked")
	}
}

// TestRedisMergeReleaseSharesWorkerFingerprintLease 需要显式 Redis 环境，只操作本测试的唯一锁 key。
func TestRedisMergeReleaseSharesWorkerFingerprintLease(t *testing.T) {
	address := os.Getenv("LINKD_TEST_REDIS_ADDRESS")
	if address == "" {
		t.Skip("set LINKD_TEST_REDIS_ADDRESS")
	}
	client := redis.NewClient(&redis.Options{Addr: address, Password: os.Getenv("LINKD_TEST_REDIS_PASSWORD"), ContextTimeoutEnabled: true})
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	cfg := scheduler.DefaultConfig()
	cfg.LockKeyPrefix = "linkd-release-test:" + strconv.Itoa(os.Getpid()) + ":" + strconv.FormatInt(time.Now().UnixNano(), 10)
	locker, err := scheduler.NewRedisLocker(client, cfg)
	if err != nil {
		t.Fatal(err)
	}
	repo := memory.New()
	a := storetest.Alert("tenant", "member", "opening", "fp", "warning")
	window := strings.Repeat("a", 64)
	a.Merge = &domain.AlertMerge{Role: "original", State: "pending", Pending: []domain.MergeWait{{WindowID: window, Policy: domain.PolicyVersion{ID: "merge", Version: 1, Digest: strings.Repeat("b", 64)}, GroupKey: strings.Repeat("c", 64), MemberEventID: a.TriggerEventID, Severity: a.Severity, Groups: []int{0}, StartedAt: a.UpdateAt, Deadline: a.UpdateAt.Add(time.Minute)}}}
	if _, err := repo.CreateAlert(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	p, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, nil, config.DefaultSeverityConfig(), lifecycle.SystemClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	key := mailbox.CorrelationKey(a.BKTenantID, a.EventSourceID, a.Fingerprint)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Del(ctx, cfg.LockKeyPrefix+":"+key).Err(); err != nil {
			t.Error(err)
		}
	})
	workerLease, err := locker.Acquire(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	run := func() error { _, err := p.ReleaseMergeWindow(t.Context(), a.BKTenantID, a.AlertID, window); return err }
	if err := withAlertLease(t.Context(), locker, key, run); !errors.Is(err, scheduler.ErrLockBusy) {
		t.Fatal("release bypassed worker lock", err)
	}
	before, err := repo.GetAlert(t.Context(), a.BKTenantID, a.AlertID)
	if err != nil || before.Alert.Merge.State != "pending" {
		t.Fatal("busy release changed alert", err)
	}
	if err := locker.Release(t.Context(), workerLease); err != nil {
		t.Fatal(err)
	}
	if err := withAlertLease(t.Context(), locker, key, run); err != nil {
		t.Fatal(err)
	}
	released, err := repo.GetAlert(t.Context(), a.BKTenantID, a.AlertID)
	if err != nil || released.Alert.Merge.State != "released" || !released.Alert.AdmittedActiveMain() {
		t.Fatal("unlocked release did not proceed", err)
	}
	if count, err := client.Exists(t.Context(), cfg.LockKeyPrefix+":"+key).Result(); err != nil || count != 0 {
		t.Fatal("release leaked lease", err)
	}
	if err := withAlertLease(t.Context(), locker, key, run); err != nil {
		t.Fatal("release retry failed", err)
	}
	replay, err := repo.GetAlert(t.Context(), a.BKTenantID, a.AlertID)
	if err != nil || replay.Version != released.Version {
		t.Fatal("repeat release changed business state", err)
	}
}
