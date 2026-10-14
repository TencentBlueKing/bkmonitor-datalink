// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle

import (
	"context"
	"errors"
	"testing"

	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type projectorFunc func(context.Context, store.StoredAlert) (store.StoredAlert, error)

func (f projectorFunc) Project(ctx context.Context, row store.StoredAlert) (store.StoredAlert, error) {
	return f(ctx, row)
}

func TestProjectionFailureDoesNotFailBusinessWrite(t *testing.T) {
	repo := memory.New()
	p := newTestProcessor(t, repo, NoopFinalHook{})
	p.initialProjectionTargets = map[string]bool{"kac": false}
	calls := 0
	p.projector = projectorFunc(func(ctx context.Context, row store.StoredAlert) (store.StoredAlert, error) {
		current, err := repo.GetAlert(ctx, row.Alert.BKTenantID, row.Alert.AlertID)
		if err != nil || current.Alert.Revision != row.Alert.Revision {
			t.Fatal("projection before business commit", err)
		}
		calls++
		return row, errors.New("projection unavailable")
	})
	e := storetest.Event("tenant", "opening", "fp", "warning")
	row, err := repo.CreateEvent(t.Context(), e)
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.ProcessEvent(t.Context(), row.StoredEvent)
	if err != nil || len(result.AlertIDs) != 1 || calls == 0 {
		t.Fatal("projection failed event", result, err)
	}
	current, err := repo.GetAlert(t.Context(), e.BKTenantID, result.AlertIDs[0])
	if err != nil || current.Alert.Revision != 1 {
		t.Fatal("Alert lost", err)
	}
	// 已完成计划的重投不再执行输出；未完成计划的恢复仍会按稳定身份补投影。
	before := calls
	latest, err := repo.GetEvent(t.Context(), e.BKTenantID, e.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ProcessEvent(t.Context(), latest); err != nil || calls != before {
		t.Fatal("completed event replayed projection", err)
	}
}
