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

	"linkd/internal/suppressioncheck"
)

type suppressionPage struct{ page suppressioncheck.Page }

func (p suppressionPage) Work(context.Context, string, int) (suppressioncheck.Page, error) {
	return p.page, nil
}

type suppressionExecute func(context.Context, suppressioncheck.Request) error

func (f suppressionExecute) Execute(ctx context.Context, r suppressioncheck.Request) error {
	return f(ctx, r)
}

func TestSuppressionRequestPageBoundsAndCancellationPreserveUnattemptedWork(t *testing.T) {
	page := suppressioncheck.Page{}
	for n := range 16 {
		c := suppressioncheck.Command{TenantID: "tenant", Kind: "aggregation", WindowID: strings.Repeat("a", 64), ExpectedEpoch: "first", ExpectedOwner: "owner", OperationID: fmt.Sprint(n), OperatorID: "tester", Reason: "检查"}
		raw, _ := json.Marshal([]string{"suppression-request:v1", c.TenantID, c.Kind, c.WindowID, c.OperationID})
		page.Items = append(page.Items, suppressioncheck.Request{ID: fmt.Sprintf("%x", sha256.Sum256(raw)), Command: c, State: "pending", CreatedAt: time.Now().UTC()})
	}
	slices.SortFunc(page.Items, func(a, b suppressioncheck.Request) int { return strings.Compare(a.Cursor(), b.Cursor()) })
	page.Next = page.Items[15].Cursor()
	var calls, active, maxSeen atomic.Int64
	execute := suppressionExecute(func(ctx context.Context, r suppressioncheck.Request) error {
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
	next, n, failed, err := checkSuppressionRequestsPage(t.Context(), suppressionPage{page}, execute, "")
	if err == nil || next != page.Next || n != 16 || failed != 1 || calls.Load() != 16 || maxSeen.Load() > 4 {
		t.Fatal(next, n, failed, err, calls.Load(), maxSeen.Load())
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	next, n, _, err = checkSuppressionRequestsPage(ctx, suppressionPage{page}, execute, "")
	if !errors.Is(err, context.Canceled) || next != "" || n != 0 || calls.Load() != 16 {
		t.Fatal("cancellation advanced or executed", next, n, err)
	}
	page.Items[1] = page.Items[0]
	if _, _, _, err := checkSuppressionRequestsPage(t.Context(), suppressionPage{page}, execute, ""); err == nil || calls.Load() != 16 {
		t.Fatal("bad page partially executed")
	}
}
