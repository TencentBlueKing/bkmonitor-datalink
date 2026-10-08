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
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type shieldCheckFunc func(context.Context, string, string) error

func (f shieldCheckFunc) CheckShield(ctx context.Context, tenant, id string) error {
	return f(ctx, tenant, id)
}

func TestShieldRoundBoundsConcurrencyAndAdvancesPastRowFailures(t *testing.T) {
	repo := memory.New()
	at := time.Date(2026, 9, 1, 0, 1, 0, 0, time.UTC)
	for i := range 17 {
		a := storetest.Alert("tenant", fmt.Sprintf("alert-%02d", i), "opening", fmt.Sprintf("fp-%d", i), "warning")
		a.Shield = domain.AlertShield{Active: true, Bindings: []domain.ShieldBinding{storetest.ShieldBinding(a.CreateAt)}, NextCheckAt: &at}
		if _, err := repo.CreateAlert(t.Context(), a); err != nil {
			t.Fatal(err)
		}
	}
	var active, maximum, calls atomic.Int64
	checker := shieldCheckFunc(func(ctx context.Context, tenant, id string) error {
		if tenant != "tenant" {
			return errors.New("unexpected tenant")
		}
		n := active.Add(1)
		defer active.Add(-1)
		calls.Add(1)
		for {
			old := maximum.Load()
			if old >= n || maximum.CompareAndSwap(old, n) {
				break
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
		if id == "alert-00" {
			return errors.New("private backend detail")
		}
		return nil
	})
	next, count, failed, err := checkShieldPage(t.Context(), repo, checker, store.ShieldWorkCursor{}, at)
	if err == nil || count != 16 || failed != 1 || next.AlertID != "alert-15" || calls.Load() != 16 || maximum.Load() > 4 {
		t.Fatalf("page count=%d failed=%d next=%+v max=%d calls=%d err=%v", count, failed, next, maximum.Load(), calls.Load(), err)
	}
	last, count, failed, err := checkShieldPage(t.Context(), repo, checker, next, at)
	if err != nil || count != 1 || failed != 0 || last.TenantID != "" {
		t.Fatal("row error starved later work", err)
	}
}
