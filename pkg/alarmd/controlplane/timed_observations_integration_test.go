// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The control plane's timed observations - the active set encoded,
// written, read and renewed, the object catalog written and renewed, a
// schedule cutover - carry their measured time on the observation, marked
// measured (Timed), as their facts do: their log lines write duration_ms,
// and a point event's does not. Each kind is seen through a publication,
// a cold read of the active set and a renewal.
func TestTheControlPlanesTimedObservationsAreMarkedTimed(t *testing.T) {
	fixture := newControlReadCacheFixture(t, "timed-observations")
	var mu sync.Mutex
	seen := map[string]int{}
	check := func(observation observability.Observation) {
		var kind string
		var facts time.Duration
		switch {
		case observation.ActiveQGSet != nil:
			kind, facts = "active_qg_set "+observation.ActiveQGSet.Operation, observation.ActiveQGSet.Duration
		case observation.ObjectCatalog != nil:
			kind, facts = "object_catalog "+observation.ObjectCatalog.Operation, observation.ObjectCatalog.Duration
		case observation.ScheduleCutover != nil:
			kind, facts = "schedule_cutover", observation.ScheduleCutover.Duration
		default:
			return
		}
		mu.Lock()
		defer mu.Unlock()
		seen[kind]++
		if !observation.Timed() || !observation.DurationKnown || observation.Duration != facts {
			t.Errorf("%s: duration %s known %v, facts %s: want the facts' time, marked measured", kind, observation.Duration,
				observation.DurationKnown, facts)
		}
	}
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) { check(observation) })
	fixture.repository.ConfigureObserver(observer)
	ctx := context.Background()

	*fixture.clock = time.Unix(180, 0)
	second, _, err := fixture.repository.PublishCatalog(ctx, catalogWithSchedule(t, validCatalog(t, 81), 60, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.reconciler.Ensure(ctx, second.Publication); err != nil {
		t.Fatal(err)
	}
	head, err := fixture.repository.LoadActivationHead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cold, _ := fixture.coldRepository(t)
	cold.ConfigureObserver(observer)
	if _, err := cold.LoadActiveQueryGroupSet(ctx, head.ActiveQGSetRef); err != nil {
		t.Fatal(err)
	}
	if err := fixture.repository.RenewCurrentActivationObjects(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, kind := range []string{"active_qg_set encode", "active_qg_set write", "active_qg_set read", "active_qg_set renew",
		"object_catalog write", "schedule_cutover"} {
		if seen[kind] == 0 {
			t.Errorf("no %s observation seen (seen %v)", kind, seen)
		}
	}
}
