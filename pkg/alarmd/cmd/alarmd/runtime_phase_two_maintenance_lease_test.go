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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
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
			f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(16, 59), []openalerts.Alert{maintenanceAlert("native", "critical")})
			catalog := f.m.catalog.(*maintenanceTestCatalog)
			ctx := context.Background()
			f.m.step(ctx)
			if catalog.reads != 1 || len(f.writer.batches) != 0 {
				t.Fatalf("inside the window: reads %d, batches %v, want one read and no close", catalog.reads, f.writer.batches)
			}
			tc.move(f.runner)
			catalog.err = errors.New("store not answering")
			f.advance(31 * time.Minute)
			f.m.step(ctx)
			if catalog.reads != 2 {
				t.Fatalf("the lease moved: reads %d, want the Plans read again", catalog.reads)
			}
			if len(f.writer.batches) != 0 {
				t.Fatalf("closed %v on Plans read under a lease that has moved", f.writer.batches)
			}
			// The read under the new lease lands: the Plans are judged again.
			catalog.err = nil
			f.m.step(ctx)
			if len(f.writer.batches) != 1 || len(f.writer.batches[0]) != 1 {
				t.Fatalf("closed %v once the Plans were read under the current lease, want the one alert", f.writer.batches)
			}
		})
	}
}
