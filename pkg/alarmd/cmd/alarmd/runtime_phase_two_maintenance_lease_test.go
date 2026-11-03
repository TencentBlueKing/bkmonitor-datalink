// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The inactive close judges a Query Group's Plans only under the lease they
// were read under: its content scope and its timeline revision, each on its
// own. The Plans are read inside their effective window; the lease then
// moves one way, the read under the new lease fails, and the window ends.
// Nothing is closed until a read under the new lease lands.
func TestTheInactiveCloseHoldsPlansReadUnderALeaseThatMoved(t *testing.T) {
	for _, tc := range []struct {
		name string
		move func(*maintenanceTestRunner)
	}{
		{"content scope", func(r *maintenanceTestRunner) { r.scope = "obj-b" }},
		{"timeline revision", func(r *maintenanceTestRunner) { r.revision++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(16, 59), maintenanceMatches)
			catalog := f.m.catalog.(*maintenanceTestCatalog)
			ctx := context.Background()
			f.m.step(ctx)
			if catalog.reads != 1 || len(f.closed()) != 0 {
				t.Fatalf("inside the window: reads %d, closes %v, want one read and no close", catalog.reads, f.closed())
			}
			tc.move(f.runner)
			catalog.err = errors.New("store not answering")
			f.advance(31 * time.Minute)
			f.m.step(ctx)
			if catalog.reads != 2 {
				t.Fatalf("the lease moved: reads %d, want the Plans read again", catalog.reads)
			}
			if len(f.closed()) != 0 {
				t.Fatalf("closed %v on Plans read under a lease that has moved", f.closed())
			}
			// The read under the new lease lands: the Plans are judged again.
			catalog.err = nil
			f.m.step(ctx)
			if len(f.closed()) != 1 {
				t.Fatalf("closed %v once the Plans were read under the current lease, want the one alert", f.closed())
			}
		})
	}
}

// The Plans are judged only inside the Segment they were read from. Read at
// 16:59 from a Segment a cutover closes at 17:10, under a lease that does not
// move again: at 17:30, out of the effective window, they are read again
// rather than judged, and the read finding no Segment closes nothing; read
// from an open Segment, or one ending after 17:30, they close the alert.
func TestTheInactiveCloseJudgesPlansOnlyInsideTheSegmentTheyWereReadFrom(t *testing.T) {
	ends := func(hour, minute int) execution.EvaluationTime {
		return execution.EvaluationTime(maintenanceTime(hour, minute).Unix())
	}
	for _, tc := range []struct {
		name   string
		until  execution.EvaluationTime
		closes bool
	}{
		{"ended before the judgement", ends(17, 10), false},
		{"ending after the judgement", ends(18, 0), true},
		{"open", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(16, 59), maintenanceMatches)
			catalog := f.m.catalog.(*maintenanceTestCatalog)
			catalog.until = tc.until
			ctx := context.Background()
			f.m.step(ctx)
			if catalog.reads != 1 || len(f.closed()) != 0 {
				t.Fatalf("inside the window: reads %d, closes %v, want one read and no close", catalog.reads, f.closed())
			}
			// The Query Group is retired at the Segment's end: a read past it
			// finds no Plan.
			catalog.err = errors.New("schedule unavailable")
			f.advance(31 * time.Minute)
			f.m.step(ctx)
			if got := len(f.closed()) == 1; got != tc.closes {
				t.Fatalf("closed %v at 17:30 with Plans read from a Segment ending %d, want a close %v", f.closed(), tc.until, tc.closes)
			}
			if wantReads := map[bool]int{false: 2, true: 1}[tc.closes]; catalog.reads != wantReads {
				t.Fatalf("reads %d, want %d", catalog.reads, wantReads)
			}
		})
	}
}
