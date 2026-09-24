// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestCoordinatorNamesStateRefusalsWithoutCommittingProgress(t *testing.T) {
	for _, test := range []struct {
		name       string
		comparison execution.ApplyVersionComparison
		apply      execution.StateApplyStatus
		want       string
		stage      string
	}{
		{"preflight conflict", execution.ApplyVersionEqual, "", contract.ReasonStateVersionConflict, "state mutation preflight"},
		{"preflight stale", execution.ApplyVersionPersistedNewer, "", contract.ReasonStateStaleVersion, "state mutation preflight"},
		{"apply conflict", execution.ApplyVersionPersistedOlder, execution.StateApplyVersionConflict, contract.ReasonStateVersionConflict, "state apply did not complete"},
		{"apply stale", execution.ApplyVersionPersistedOlder, execution.StateApplyStale, contract.ReasonStateStaleVersion, "state apply did not complete"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPlanIsolationFixture(t, nil)
			fixture.loaded.Items[0].VersionComparison = test.comparison
			// Equal ApplyVersion with a different mutation digest is a real
			// preflight conflict, rather than a constructed error value.
			fixture.loaded.Items[0].PersistedMutationDigest = "another-mutation"
			state := &refusingStateApplyStore{StateStore: fixture.coordinator.ports.State, status: test.apply}
			if test.apply != "" {
				fixture.coordinator.ports.State = state
			}
			result, err := fixture.coordinator.finalizePreparedWithGaps(context.Background(), fixture.request,
				fixture.header, fixture.bindings, fixture.loaded, execution.GapLoadResult{}, fixture.evaluated,
				nil, execution.QueryAvailabilityUnknown, seriesCensus{}, nil)
			if err == nil || result.Completed || fixture.base.progressCommits != 0 {
				t.Fatalf("refusal advanced Progress: result=%+v commits=%d err=%v", result, fixture.base.progressCommits, err)
			}
			if reason, named := StateConflictReason(err); !named || string(reason) != test.want {
				t.Fatalf("reason=%s named=%v, want %s; err=%v", reason, named, test.want, err)
			}
			var refusal *StateConflictError
			if !errors.As(err, &refusal) || refusal.Stage != test.stage {
				t.Fatalf("wrong refusal site: %v", err)
			}
			wantApplyCalls := 0
			if test.apply != "" {
				wantApplyCalls = 1
			}
			if state.calls != wantApplyCalls || len(fixture.base.stateApplied) != 0 {
				t.Fatalf("apply calls=%d want=%d, successful writes=%v", state.calls, wantApplyCalls, fixture.base.stateApplied)
			}
			// The chunk's own line names the refusal too, with the error's
			// words: it used to read internal_unknown beside an error_type
			// that already said which refusal it was.
			if test.apply != "" {
				applied := 0
				for _, observation := range fixture.observations {
					if observation.Stage != observability.StageStateApplied {
						continue
					}
					applied++
					if string(observation.ReasonCode) != test.want || observation.Err == nil || !strings.Contains(observation.Err.Error(), test.stage) {
						t.Fatalf("state_applied observation = reason %s err %v, want %s with the refusal's words", observation.ReasonCode, observation.Err, test.want)
					}
				}
				if applied == 0 {
					t.Fatalf("no state_applied observation for the refused chunk")
				}
				return
			}
			// The preflight line names the refusal the same way, and a
			// conflict decided there is one kind only: the mutation's
			// expectation came from this very view, so the revision cannot
			// differ; what differs is the statement for the same window.
			var refused *observability.Observation
			for index := range fixture.observations {
				observation := &fixture.observations[index]
				if observation.Stage == observability.StageMutationCompared && observation.Err != nil {
					refused = observation
					break
				}
			}
			if refused == nil || string(refused.ReasonCode) != test.want {
				t.Fatalf("preflight refusal line = %+v, want reason %s", refused, test.want)
			}
			if test.want != contract.ReasonStateVersionConflict {
				if refusal.Kind != "" || refused.StateVersionConflict != nil {
					t.Fatalf("stale refusal carries a conflict kind: err=%+v facts=%+v", refusal, refused.StateVersionConflict)
				}
				return
			}
			view := fixture.loaded.Items[0]
			if refusal.Kind != execution.StateVersionConflictSameVersionOtherStatement || refusal.ExpectedRevision != view.BlobRevision ||
				refusal.StoredRevision != view.BlobRevision || refusal.VersionComparison != execution.ApplyVersionEqual {
				t.Fatalf("preflight conflict = %+v, want same_version_other_statement at revision %d both sides, PERSISTED_EQUAL", refusal, view.BlobRevision)
			}
			key := observability.StateVersionConflictKey{Site: observability.StateAlreadyAppliedAtPreflight, Kind: observability.StateVersionConflictSameVersionOtherStatement}
			if facts := refused.StateVersionConflict; facts == nil || len(facts.Counts) != 1 || facts.Counts[key] != 1 ||
				len(facts.Samples) != 1 || facts.Samples[0].SeriesIdentity != string(view.Identity.SeriesIdentityDigest) {
				t.Fatalf("preflight conflict facts = %+v, want one %+v with the refused series as sample", refused.StateVersionConflict, key)
			}
		})
	}
}

type refusingStateApplyStore struct {
	execution.StateStore
	status execution.StateApplyStatus
	calls  int
}

func (store *refusingStateApplyStore) ApplyRuntime(_ context.Context, request execution.StateApplyRequest) (execution.StateApplyResult, error) {
	store.calls++
	items := make([]execution.StateApplyItemResult, len(request.Items))
	for index, mutation := range request.Items {
		items[index] = execution.StateApplyItemResult{Identity: mutation.Identity, Status: store.status}
	}
	return execution.StateApplyResult{Items: items}, nil
}
