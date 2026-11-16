// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.
package worker

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// movedScopeProgress refuses every commit as the store does when the record
// names another content scope than the one the Slot ran under.
type movedScopeProgress struct{ execution.ProgressStore }

func (movedScopeProgress) CommitProgress(context.Context, execution.ProgressCommitRequest) (execution.ProgressCommitResult, error) {
	return execution.ProgressCommitResult{Status: execution.ProgressContentMoved}, nil
}

// A commit refused for a moved scope names CONTENT_SCOPE_MOVED on the Slot's
// error, as a stale owner names the handover; it used to be read as Redis
// unavailable.
func TestAProgressCommitRefusedForAMovedScopeNamesIt(t *testing.T) {
	fixture := newPlanIsolationFixture(t, nil)
	commitReady(fixture)
	fixture.coordinator.ports.Progress = movedScopeProgress{fixture.coordinator.ports.Progress}
	_, err := fixture.coordinator.finalizePreparedWithGaps(context.Background(), fixture.request, fixture.header, fixture.bindings,
		fixture.loaded, execution.GapLoadResult{}, fixture.evaluated, nil, queryAvailabilityEvidence{}, seriesCensus{}, nil)
	if refusal, refused := ownership.RefusalReason(err); !refused || refusal != contract.ReasonContentScopeMoved {
		t.Fatalf("Slot error = %v, want it to carry %s", err, contract.ReasonContentScopeMoved)
	}
}
