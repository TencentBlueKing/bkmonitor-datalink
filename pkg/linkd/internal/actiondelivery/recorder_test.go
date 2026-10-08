// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package actiondelivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"linkd/internal/domain"
)

func TestRecorderWithoutDeliveryDependenciesRetainsOriginalIntent(t *testing.T) {
	tasks := &taskMemory{rows: map[string]StoredTask{}}
	locker := &targetLocker{held: map[string]bool{}}
	recorder, err := NewRecorder(tasks, locker, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range []struct {
		tasks  RecordingStore
		locker TargetLocker
		now    func() time.Time
	}{{nil, locker, time.Now}, {tasks, nil, time.Now}, {tasks, locker, nil}} {
		if _, err := NewRecorder(args.tasks, args.locker, args.now); !errors.Is(err, ErrInvalid) {
			t.Fatal("missing recorder port accepted", err)
		}
	}
	a := actionAlert("tenant", 1)
	target := a.Projection.Targets["kac"]
	target.ActionEnabled = true
	a.Projection.Targets["kac"] = target
	a.ActionPending, err = domain.NewAlertActionIntent(a, "source_event", a.LatestEventID)
	if err != nil {
		t.Fatal(err)
	}
	tasks.visibilityErr = ErrBusy
	if err := recorder.RecordAction(t.Context(), a, *a.ActionPending); !errors.Is(err, ErrBusy) {
		t.Fatal("unsearchable task confirmed", err)
	}
	tasks.visibilityErr = nil
	for range 2 {
		if err := recorder.RecordAction(t.Context(), a, *a.ActionPending); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := tasks.List(t.Context(), Query{TenantID: "tenant", Limit: 4})
	if err != nil || len(rows) != 1 || rows[0].Task.Progress.Attempts != 0 || rows[0].Task.SourceVersion != 4 {
		t.Fatal("enqueue changed/delivered original task", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := recorder.Record(ctx, a, "kac", Cause{Type: "source_event", ID: a.LatestEventID}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel swallowed", err)
	}
	//nolint:staticcheck // SA1012: 显式覆盖公开入口的 nil Context 拒绝分支，确保返回错误而不是 panic。
	if _, err := recorder.Record(nil, a, "kac", Cause{Type: "source_event", ID: a.LatestEventID}); !errors.Is(err, ErrInvalid) {
		t.Fatal("nil context accepted", err)
	}
}
