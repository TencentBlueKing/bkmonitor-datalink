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
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/shieldcheck"
)

type requestWorkPage struct{ page shieldcheck.Page }

func (w requestWorkPage) Work(context.Context, string, int) (shieldcheck.Page, error) {
	return w.page, nil
}

type executeRequest func(context.Context, shieldcheck.Request) error

func (f executeRequest) Execute(ctx context.Context, r shieldcheck.Request) error { return f(ctx, r) }

func TestShieldRequestPageBoundsExecutionAndAdvancesPastFailures(t *testing.T) {
	page := shieldcheck.Page{Next: "next"}
	for i := range 16 {
		c := shieldcheck.Command{TenantID: "tenant", AlertID: fmt.Sprintf("alert-%02d", i), OperationID: "op", ExpectedRevision: 1, OperatorID: "tester", Reason: "复查"}
		raw, _ := json.Marshal([]string{"shield-check-request", c.TenantID, c.AlertID, c.OperationID})
		page.Items = append(page.Items, shieldcheck.Request{ID: fmt.Sprintf("%x", sha256.Sum256(raw)), Command: c, State: "pending", CreatedAt: time.Now().UTC()})
	}
	var active, maxSeen, calls atomic.Int64
	executor := executeRequest(func(ctx context.Context, r shieldcheck.Request) error {
		n := active.Add(1)
		defer active.Add(-1)
		calls.Add(1)
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
		if r.Command.AlertID == "alert-00" {
			return errors.New("row failed")
		}
		return nil
	})
	next, count, failed, err := checkShieldRequestsPage(t.Context(), requestWorkPage{page}, executor, "")
	if err == nil || count != 16 || failed != 1 || next != "next" || calls.Load() != 16 || maxSeen.Load() > 4 {
		t.Fatal(next, count, failed, err, calls.Load(), maxSeen.Load())
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, count, failed, err = checkShieldRequestsPage(ctx, requestWorkPage{page}, executor, "")
	if err == nil || count != 16 || failed != 16 || calls.Load() != 16 {
		t.Fatal("canceled queue executed", count, failed, err)
	}
	page.Next = "same"
	if _, _, _, err := checkShieldRequestsPage(t.Context(), requestWorkPage{page}, executor, "same"); err == nil {
		t.Fatal("non advancing cursor")
	}
}
