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
	"sync"
	"testing"
	"time"

	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/policy"
)

func TestShieldCheckerSharesFourSlotsAndCancelsQueue(t *testing.T) {
	entered := make(chan struct{}, 4)
	finish := make(chan struct{})
	var wg sync.WaitGroup
	checker := &ShieldChecker{slots: make(chan struct{}, 4), run: func(ctx context.Context, tenant, id string, expected int64) (lifecycle.ShieldCheckReport, error) {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 10*time.Second {
			t.Error("unbounded check")
		}
		entered <- struct{}{}
		select {
		case <-ctx.Done():
			return lifecycle.ShieldCheckReport{Outcome: "failed"}, ctx.Err()
		case <-finish:
			return lifecycle.ShieldCheckReport{Outcome: "inactive"}, nil
		}
	}}
	for range 4 {
		wg.Go(func() {
			if err := checker.CheckShield(t.Context(), "tenant", "alert"); err != nil {
				t.Error(err)
			}
		})
	}
	for range 4 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("check did not start")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := checker.CheckShield(ctx, "tenant", "alert"); !errors.Is(err, context.Canceled) {
		t.Fatal("queue ignored cancellation", err)
	}
	close(finish)
	wg.Wait()
	if len(checker.slots) != 0 {
		t.Fatal("slot leaked")
	}
}

func TestShieldCheckerSafeErrorsPreservePartialBusinessChange(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{{lifecycle.ErrShieldCheckStale, "revision_changed"}, {policy.ErrAccess, "scope_mismatch"}, {scheduler.ErrLockBusy, "alert_busy"}, {context.DeadlineExceeded, "check_timeout"}, {errors.New("private token"), "dependency_failed"}} {
		checker := &ShieldChecker{slots: make(chan struct{}, 4), run: func(context.Context, string, string, int64) (lifecycle.ShieldCheckReport, error) {
			return lifecycle.ShieldCheckReport{Outcome: "changed", Changed: true}, tc.err
		}}
		result, err := checker.check(t.Context(), "tenant", "alert", 0, "")
		if !errors.Is(err, tc.err) || result.ErrorCode != tc.code || !result.Report.Changed {
			t.Fatal(result, err)
		}
	}
}

func TestHintCheckUsesSameSlotsAndDistinctDiagnostic(t *testing.T) {
	checker := &ShieldChecker{slots: make(chan struct{}, 4), run: func(context.Context, string, string, int64) (lifecycle.ShieldCheckReport, error) {
		return lifecycle.ShieldCheckReport{Outcome: "failed"}, errors.New("private backend")
	}}
	result, err := checker.checkFrom(t.Context(), "tenant", "alert", 0, "", "hint")
	if err == nil || result.Trigger != "hint" || result.RequestID != "" || result.ErrorCode != "dependency_failed" || result.Validate() != nil {
		t.Fatal(result, err)
	}
	for range 4 {
		checker.slots <- struct{}{}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := checker.CheckHint(ctx, "tenant", "alert"); !errors.Is(err, context.Canceled) {
		t.Fatal("hint bypassed shared slots", err)
	}
}
