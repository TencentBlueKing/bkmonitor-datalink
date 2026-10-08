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
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/merge"
)

type mergeRequestsPageFixture struct{ page merge.RetryPage }

func (p mergeRequestsPageFixture) RetryWork(context.Context, string, int) (merge.RetryPage, error) {
	return p.page, nil
}

type mergeRequestsExecuteFixture func(context.Context, merge.RetryRequest) error

func (f mergeRequestsExecuteFixture) Execute(ctx context.Context, r merge.RetryRequest) error {
	return f(ctx, r)
}

func TestMergeRequestPageBoundsAndCancellationPreserveUnattemptedWork(t *testing.T) {
	id, err := domain.MergeDecisionID("tenant", strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	page := merge.RetryPage{}
	for n := range 16 {
		c := merge.RetryCommand{TenantID: "tenant", Kind: "decisions", TargetID: id, ExpectedToken: strings.Repeat("b", 64), OperationID: fmt.Sprint(n), OperatorID: "tester", Reason: "检查"}
		raw, _ := json.Marshal([]string{"merge-request:v1", c.TenantID, c.Kind, c.TargetID, c.OperationID})
		page.Items = append(page.Items, merge.RetryRequest{ID: fmt.Sprintf("%x", sha256.Sum256(raw)), Command: c, WindowID: strings.Repeat("a", 64), State: "pending", CreatedAt: time.Now().UTC()})
	}
	slices.SortFunc(page.Items, func(a, b merge.RetryRequest) int { return strings.Compare(a.Cursor(), b.Cursor()) })
	page.Next = page.Items[15].Cursor()
	var calls, active, maxSeen atomic.Int64
	execute := mergeRequestsExecuteFixture(func(ctx context.Context, r merge.RetryRequest) error {
		calls.Add(1)
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := maxSeen.Load()
			if old >= n || maxSeen.CompareAndSwap(old, n) {
				break
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
		}
		if r.Command.OperationID == "0" {
			return errors.New("row failed")
		}
		return nil
	})
	next, n, failed, err := checkMergeRequestsPage(t.Context(), mergeRequestsPageFixture{page}, execute, "")
	if err == nil || next != page.Next || n != 16 || failed != 1 || calls.Load() != 16 || maxSeen.Load() > 4 {
		t.Fatal(next, n, failed, err, calls.Load(), maxSeen.Load())
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	next, n, _, err = checkMergeRequestsPage(ctx, mergeRequestsPageFixture{page}, execute, "")
	if !errors.Is(err, context.Canceled) || next != "" || n != 0 || calls.Load() != 16 {
		t.Fatal("cancellation advanced or executed", next, n, err)
	}
	page.Items[1] = page.Items[0]
	if _, _, _, err := checkMergeRequestsPage(t.Context(), mergeRequestsPageFixture{page}, execute, ""); err == nil || calls.Load() != 16 {
		t.Fatal("bad page partially executed")
	}
}
