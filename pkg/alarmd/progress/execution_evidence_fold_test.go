// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package progress

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A query-free completion opens a gap unless an earlier attempt evaluated every
// Plan the Slot was going to.
//
// The two halves are one table because they are one rule seen from both sides,
// and each is load-bearing in a different direction. Dropping the gap for a
// fully executed Slot is the defect this change exists to fix: the Slot
// detected and alerted, and only its bookkeeping was lost. Keeping it for a
// partly executed one is what stops the fix going too far -- some Plans really
// were not evaluated, and the consumers that read a gap as fact have to keep
// seeing it. One gap too many costs an extra no-data or expired-range
// evaluation; one too few is a detection that never happens and that nothing
// reports.
func TestAQueryFreeCompletionOpensAGapUnlessEveryPlanWasAlreadyEvaluated(t *testing.T) {
	gapSkipped := execution.ReasonCode(contract.ReasonGapSkipped)
	for _, test := range []struct {
		name     string
		evidence *execution.ExecutionEvidence
		wantGap  bool
	}{
		{
			name:     "every Plan was evaluated by an earlier attempt",
			evidence: &execution.ExecutionEvidence{Kind: execution.EvidenceStateApplied, PlansApplied: 3, PlansTotal: 3},
			wantGap:  false,
		},
		{
			name:     "some Plans were evaluated and some were not",
			evidence: &execution.ExecutionEvidence{Kind: execution.EvidenceStateApplied, PlansApplied: 2, PlansTotal: 3},
			wantGap:  true,
		},
		{
			name:     "one of three is still a gap",
			evidence: &execution.ExecutionEvidence{Kind: execution.EvidenceStateApplied, PlansApplied: 1, PlansTotal: 3},
			wantGap:  true,
		},
		{
			name:     "no earlier attempt got that far",
			evidence: &execution.ExecutionEvidence{Kind: execution.EvidenceNoneFound, PlansTotal: 3},
			wantGap:  true,
		},
		{
			name:     "the record could not be read",
			evidence: &execution.ExecutionEvidence{Kind: execution.EvidenceUnreadable, PlansTotal: 3},
			wantGap:  true,
		},
		{
			// A build or a deployment without the mark. It must behave exactly
			// as it did before the evidence existed, or the rollout changes
			// what every unfinished Slot records.
			name:     "the completion carries no evidence at all",
			evidence: nil,
			wantGap:  true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &controlFake{missing: true}
			store := mustStore(t, fake)
			identity := execution.ProgressIdentity{QueryGroup: "q"}
			fence := execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}
			projection := progressProjection()
			if _, err := store.BeginSlot(context.Background(), execution.ProgressBeginRequest{
				Identity: identity, OwnerFence: fence, Projection: projection,
			}); err != nil {
				t.Fatal(err)
			}
			result, err := store.CommitProgress(context.Background(), execution.ProgressCommitRequest{
				Identity: identity, OwnerFence: fence, ExpectedNextSlot: 60, Projection: projection,
				Completion: execution.SlotCompletion{
					Contract: progressContract(), Kind: execution.CompletionGapSkipped,
					Result: observability.ResultDegraded, ReasonCode: gapSkipped, Evidence: test.evidence,
				},
			})
			if err != nil || result.Status != execution.ProgressCommitted {
				t.Fatalf("CommitProgress() = (%+v, %v)", result, err)
			}
			loaded, err := store.LoadProgress(context.Background(), identity)
			if err != nil || loaded.Progress == nil {
				t.Fatalf("LoadProgress() = (%+v, %v)", loaded, err)
			}
			gap := loaded.Progress.CurrentOrRecentGap
			if test.wantGap && gap == nil {
				t.Fatal("no gap was opened. Plans this Slot was going to evaluate were not evaluated, " +
					"and the consumers that read a gap as fact will not see it")
			}
			if !test.wantGap && gap != nil {
				t.Fatalf("a gap was opened for a Slot every Plan of which was already evaluated and "+
					"alerted: %+v", gap)
			}
			// The kind is recorded either way: the Slot did miss its window,
			// and the readers of pruned cursors and expired ranges read the
			// gap rather than the kind.
			if loaded.Progress.LastCompletionKind != execution.CompletionGapSkipped {
				t.Fatalf("last completion = %q, want GAP_SKIPPED whatever the evidence said",
					loaded.Progress.LastCompletionKind)
			}
			if test.wantGap && gap != nil && test.evidence != nil {
				// The gap carries what the completion knew, so the page reads
				// it from the record instead of asking a tracker that only
				// sees the present.
				if gap.Evidence == nil || *gap.Evidence != *test.evidence {
					t.Fatalf("gap evidence = %+v, want %+v carried onto the record",
						gap.Evidence, test.evidence)
				}
			}
		})
	}
}
