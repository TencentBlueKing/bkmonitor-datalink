// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestObservationDirectoryFullIdentityAndSourceTruncation(t *testing.T) {
	at := time.Now()
	s := &StrategyDirectorySnapshot{ObservedAt: at, Revision: "v1", Complete: true, SourceComplete: true, byStrategy: map[string][]int{"s": {0, 1}}}
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		s.Rows = append(s.Rows, StrategyDirectoryRow{Identity: execution.PlanIdentity{TenantID: tenant, BusinessID: "b", StrategyID: "s"}, QueryGroup: execution.QueryGroupIdentity(tenant), Role: string(execution.ActivationCurrent)})
	}
	for i := 0; i < 4; i++ {
		s.Unattributed = append(s.Unattributed, ObjectDisposition{SourceID: "s", Reason: "WITHHELD"})
	}
	d := &ObservationDirectory{limits: DirectoryLimits{FreshFor: time.Minute}}
	d.state.Store(s)
	if _, err := d.ResolveCurrent(at, "", "", "s", ""); !errors.Is(err, ErrObservationAmbiguous) {
		t.Fatalf("cross-tenant identity merged: %v", err)
	}
	if selected, err := d.ResolveCurrent(at, "tenant-b", "b", "s", ""); err != nil || selected.Identity.TenantID != "tenant-b" {
		t.Fatalf("selection %+v %v", selected, err)
	}
	page := d.Page(at, "", "", "s", 0, 1)
	if page.SourceMatchedTotal != 4 || !page.SourceTruncated || len(page.Unattributed) != 1 || !page.SourceComplete {
		t.Fatalf("unmarked source projection truncation %+v", page)
	}
	all := d.Page(at, "", "", "", 0, 1)
	if len(all.Unattributed) != 0 {
		t.Fatal("normal page copied source audit")
	}
	s.Complete = false
	if _, err := d.ResolveCurrent(at, "tenant-b", "b", "s", ""); !errors.Is(err, ErrSnapshotUnavailable) {
		t.Fatalf("incomplete directory proved unique: %v", err)
	}
	if _, err := d.ResolveCurrent(at, "tenant-b", "b", "s", "tenant-b"); err != nil {
		t.Fatalf("fully specified positive observation rejected: %v", err)
	}
}

func BenchmarkObservationDirectoryCachedStrategyPage(b *testing.B) {
	for _, size := range []int{1000, 10000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			at := time.Now()
			d := &ObservationDirectory{limits: DirectoryLimits{FreshFor: time.Minute}}
			s := &StrategyDirectorySnapshot{ObservedAt: at, Complete: true, byStrategy: map[string][]int{}}
			for i := 0; i < size; i++ {
				id := fmt.Sprint(i)
				s.byStrategy[id] = []int{i}
				s.Rows = append(s.Rows, StrategyDirectoryRow{Identity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "business", StrategyID: id}})
			}
			d.state.Store(s)
			id := fmt.Sprint(size / 2)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				p := d.Page(at, "tenant", "business", id, 0, 20)
				if len(p.Rows) != 1 {
					b.Fatal("missing selected row")
				}
			}
		})
	}
}
