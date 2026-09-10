// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// TestFinalizePreparedSendsThePlanRetentionToTheStore pins the one link that
// lets the store size a write TTL at all. Without it the store has no way to
// tell how long a key must survive and falls back on the configured ceiling,
// which is what left keys alive for a month after their series went silent.
// The retention has to reach both calls: admission refuses a Plan no TTL can
// satisfy before any event is written, and apply stores the derived TTL.
func TestFinalizePreparedSendsThePlanRetentionToTheStore(t *testing.T) {
	// The event sink refuses the first Plan retryably, so that Plan is admitted
	// but never applied while its healthy sibling completes: the fixture covers
	// both store calls without needing a Progress commit.
	fixture := newPlanIsolationFixture(t, &retryablePlanEventError{err: errors.New("broker ACK unavailable")})
	if _, err := fixture.coordinator.finalizePrepared(
		context.Background(), fixture.request, fixture.header, fixture.bindings, fixture.loaded, fixture.evaluated,
	); err != nil {
		t.Fatalf("finalizePrepared() error = %v", err)
	}
	retention := func(due execution.DuePlan) []execution.StateRetentionRequirement {
		t.Helper()
		want, err := execution.DeriveStateRetentionRequirement(due.CompiledPlan)
		if err != nil {
			t.Fatalf("DeriveStateRetentionRequirement() error = %v", err)
		}
		if len(want) == 0 {
			t.Fatal("the fixture Plan carries no Level retention")
		}
		return want
	}
	failed, healthy := fixture.header.DuePlans[0], fixture.header.DuePlans[1]
	wantAdmitted := [][]execution.StateRetentionRequirement{retention(failed), retention(healthy)}
	if !reflect.DeepEqual(fixture.base.admittedRetention, wantAdmitted) {
		t.Fatalf("admitted retention = %+v, want one derived set per due Plan (%+v)",
			fixture.base.admittedRetention, wantAdmitted)
	}
	wantApplied := [][]execution.StateRetentionRequirement{retention(healthy)}
	if !reflect.DeepEqual(fixture.base.appliedRetention, wantApplied) {
		t.Fatalf("applied retention = %+v, want the healthy Plan's derived set (%+v)",
			fixture.base.appliedRetention, wantApplied)
	}
}
