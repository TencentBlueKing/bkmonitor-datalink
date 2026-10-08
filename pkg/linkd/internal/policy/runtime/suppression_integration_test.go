// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type policyTestClock struct{ at time.Time }

func (c *policyTestClock) Now() time.Time { return c.at }

type fixedPolicySnapshot struct{ release policy.Release }

func (s fixedPolicySnapshot) Snapshot(_ context.Context, _ domain.Event, at time.Time) (*store.PolicyContext, error) {
	return &store.PolicyContext{EvaluatedAt: at, Releases: []store.PolicyReleaseRef{{Kind: "suppression", ID: s.release.ID, Version: s.release.Version, Digest: s.release.Compiled.Digest}}}, nil
}

func realSuppressionState(t *testing.T) *redisstate.Store {
	t.Helper()
	address := os.Getenv("LINKD_TEST_REDIS_ADDRESS")
	if address == "" {
		t.Skip("set LINKD_TEST_REDIS_ADDRESS for actual policy/lifecycle integration")
	}
	client := redis.NewClient(&redis.Options{Addr: address, Password: os.Getenv("LINKD_TEST_REDIS_PASSWORD"), ContextTimeoutEnabled: true, PoolSize: 4, MaxRetries: -1})
	deployment := fmt.Sprintf("suppression-runtime-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	state, err := redisstate.New(client, deployment)
	if err != nil {
		_ = client.Close()
		t.Fatal(err)
	}
	if err := client.Ping(t.Context()).Err(); err != nil {
		_ = client.Close()
		t.Fatal(err)
	}
	// 只回收本次测试的独立部署命名空间；不用 FLUSHDB 或业务键全局匹配。
	raw, _ := json.Marshal([]string{"deployment", deployment})
	sum := sha256.Sum256(raw)
	prefix := fmt.Sprintf("linkd:policies:%x:*", sum)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var cursor uint64
		for {
			keys, next, err := client.Scan(ctx, cursor, prefix, 100).Result()
			if err != nil {
				t.Error(err)
				break
			}
			if len(keys) > 0 {
				if err := client.Del(ctx, keys...).Err(); err != nil {
					t.Error(err)
					break
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	return state
}

func TestRedisSuppressionLifecycleThresholdBypassAndTerminalReset(t *testing.T) {
	state := realSuppressionState(t)
	release := clipRelease(t)
	repo := memory.New()
	clock := &policyTestClock{at: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	suppressor := &Suppressor{Releases: &suppressionReleaseReader{release: release}, Targets: suppressionTargets{}, State: state}
	p, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, nil, config.DefaultSeverityConfig(), clock, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithPolicySnapshotter(fixedPolicySnapshot{release}), lifecycle.WithNewAlertSuppressor(suppressor), lifecycle.WithSeverityUpgradePolicy("update_current"))
	if err != nil {
		t.Fatal(err)
	}
	run := func(id, level string, action domain.EventAction) store.StoredEvent {
		t.Helper()
		event := storetest.Event("tenant", id, "fp", level)
		event.Evaluations[0].Action = action
		event.Dimensions["bk_biz_id"] = domain.NewStringScalar("2")
		event.Labels["source_id"] = domain.NewStringScalar("source")
		event.OccurredAt = clock.at
		event.ProducedAt = clock.at
		event.ReceivedAt = clock.at
		event.CreateAt = clock.at
		created, err := repo.CreateEvent(t.Context(), event)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = p.ProcessEvent(t.Context(), created.StoredEvent); err != nil {
			t.Fatal(err)
		}
		saved, err := repo.GetEvent(t.Context(), event.BKTenantID, id)
		if err != nil {
			t.Fatal(err)
		}
		clock.at = clock.at.Add(time.Second)
		return saved
	}
	for _, id := range []string{"first", "second"} {
		s := run(id, "warning", domain.EventActionTriggered)
		if s.Processing.State != domain.EventProcessStateSuppressed || len(s.Event.RelatedAlertIDs) != 0 {
			t.Fatalf("pre-threshold created Alert %+v", s)
		}
	}
	admitted := run("third", "warning", domain.EventActionTriggered)
	if admitted.Processing.State != domain.EventProcessStateAccepted || len(admitted.Event.RelatedAlertIDs) != 1 {
		t.Fatalf("threshold failed %+v", admitted.Processing)
	}
	// 活动 Alert 升级不访问门槛，即使策略运行依赖此时全部不可用。
	suppressor.Releases = nil
	suppressor.Catalog = nil
	suppressor.State = nil
	upgrade := run("upgrade", "critical", domain.EventActionTriggered)
	if upgrade.Processing.PolicyDecision.Suppression.BypassReason != "active_alert" || upgrade.Event.RelatedAlertIDs[0] != admitted.Event.RelatedAlertIDs[0] {
		t.Fatal("active upgrade entered suppression")
	}
	a, err := repo.GetAlert(t.Context(), "tenant", admitted.Event.RelatedAlertIDs[0])
	if err != nil || a.Alert.Severity != "critical" || a.Alert.TriggerEventID != "third" {
		t.Fatal("upgrade lost opening facts", err)
	}
	suppressor.State = state
	suppressor.Releases = &suppressionReleaseReader{release: release}
	run("recover", "critical", domain.EventActionResolved)
	after := run("after-recovery", "warning", domain.EventActionTriggered)
	if after.Processing.PolicyDecision.Suppression.Evaluations[0].Steps[0].Count != 1 {
		t.Fatal("terminal did not reset owner count")
	}
	run("orphan-recover", "warning", domain.EventActionResolved)
	after = run("after-orphan", "warning", domain.EventActionTriggered)
	if after.Processing.PolicyDecision.Suppression.Evaluations[0].Steps[0].Count != 1 {
		t.Fatal("orphan recovery did not reset unowned count")
	}
}
