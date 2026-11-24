// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplaneprocess

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/policy"
)

type recoverPoliciesFunc func(context.Context, string, int) (string, error)

func (f recoverPoliciesFunc) RecoverPage(ctx context.Context, after string, limit int) (string, error) {
	return f(ctx, after, limit)
}

func TestPolicyRecoveryKeepsProgressAndReportsFailure(t *testing.T) {
	registry := taskCatalog(config.Config{}, 0)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := recoverPoliciesFunc(func(ctx context.Context, after string, limit int) (string, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 10*time.Second || after != "before" || limit != policy.MaxPageSize {
			t.Fatal("unbounded or invalid recovery call")
		}
		return "next", errors.New("dependency failed")
	})
	next := reconcilePolicyPublication(t.Context(), service, "before", logger, registry, nil)
	if next != "next" {
		t.Fatal("progress dropped")
	}
	found := false
	for _, task := range registry.Snapshot().Tasks {
		if task.ID == "policy-publication" {
			found = true
			if task.Execution.Failed != 1 || task.Execution.ErrorCode != "policy_publication_recovery_failed" {
				t.Fatalf("missing failure %+v", task.Execution)
			}
		}
	}
	if !found {
		t.Fatal("missing task")
	}
}

func TestPolicyRecoveryStopsOnCancellation(t *testing.T) {
	registry := taskCatalog(config.Config{}, 0)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	service := recoverPoliciesFunc(func(ctx context.Context, _ string, _ int) (string, error) { calls++; cancel(); return "", ctx.Err() })
	if err := runPolicyPublication(ctx, service, logger, registry, nil); err != nil || calls != 1 {
		t.Fatalf("stop %d %v", calls, err)
	}
	for _, task := range registry.Snapshot().Tasks {
		if task.ID == "policy-publication" && (task.Execution.Failed != 0 || task.Execution.Canceled != 1) {
			t.Fatalf("cancellation counted as failure %+v", task.Execution)
		}
	}
}
