// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package merge

import (
	"context"
	"errors"
	"testing"
	"time"

	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	"linkd/internal/store"
)

type windowReadFunc func(context.Context, string, string) (redisstate.MergeWindow, bool, error)

func (f windowReadFunc) ReadMergeWindow(ctx context.Context, tenant, id string) (redisstate.MergeWindow, bool, error) {
	return f(ctx, tenant, id)
}

func TestLostWaitRequiresMissingRedisAndNoDurableDecision(t *testing.T) {
	for _, mode := range []string{"lost", "before deadline", "durable decision", "window exists", "redis error", "missing member", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			f := newRelationFixture(t)
			current, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, "a")
			if err != nil {
				t.Fatal(err)
			}
			wait := current.Alert.Merge.Pending[0]
			j, _ := newJournal(t)
			if mode == "durable decision" {
				j = f.journal
			}
			at := wait.Deadline
			if mode == "before deadline" {
				at = at.Add(-time.Nanosecond)
			}
			failure := errors.New("redis unavailable")
			windows := windowReadFunc(func(context.Context, string, string) (redisstate.MergeWindow, bool, error) {
				if mode == "redis error" {
					return redisstate.MergeWindow{}, false, failure
				}
				return redisstate.MergeWindow{ID: wait.WindowID, TenantID: current.Alert.BKTenantID, Policy: wait.Policy, GroupKey: wait.GroupKey}, mode == "window exists", nil
			})
			called := false
			releaser := releaseFunc(func(ctx context.Context, tenant, id, window string) (store.StoredAlert, error) {
				called = true
				if mode == "missing member" {
					return store.StoredAlert{}, store.ErrNotFound
				}
				return f.processor.ReleaseMergeWindow(ctx, tenant, id, window)
			})
			ctx := t.Context()
			if mode == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			released, err := j.ReleaseLostWait(ctx, current, wait.WindowID, windows, releaser, at)
			switch mode {
			case "lost":
				if err != nil || !released || !called || len(f.action.inputs) != 1 {
					t.Fatal("lost expired wait not released", err)
				}
			case "redis error":
				if !errors.Is(err, failure) || released || called {
					t.Fatal("redis failure became lost window", err)
				}
			case "missing member":
				if !errors.Is(err, store.ErrNotFound) || released {
					t.Fatal("real member disappearance ignored", err)
				}
			case "cancelled":
				if !errors.Is(err, context.Canceled) || released || called {
					t.Fatal("cancellation ignored", err)
				}
			default:
				if err != nil || released || called || len(f.action.inputs) != 0 {
					t.Fatal("protected wait released", err)
				}
			}
			if mode == "durable decision" {
				other, err := j.GetByWindow(t.Context(), current.Alert.BKTenantID, wait.WindowID)
				if err != nil || other.Decision.Progress.Phase != "linking" {
					t.Fatal("durable decision changed", err)
				}
			}
		})
	}
}

func TestLostWaitRejectsCrossTenantWindow(t *testing.T) {
	f := newRelationFixture(t)
	current, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, "a")
	if err != nil {
		t.Fatal(err)
	}
	j, _ := newJournal(t)
	wait := current.Alert.Merge.Pending[0]
	windows := windowReadFunc(func(context.Context, string, string) (redisstate.MergeWindow, bool, error) {
		return redisstate.MergeWindow{TenantID: "other", ID: wait.WindowID}, true, nil
	})
	if _, err := j.ReleaseLostWait(t.Context(), current, wait.WindowID, windows, f.processor, wait.Deadline); !errors.Is(err, policy.ErrAccess) {
		t.Fatal("cross-tenant cache allowed", err)
	}
	if len(f.action.inputs) != 0 {
		t.Fatal("cross tenant caused release")
	}
}
