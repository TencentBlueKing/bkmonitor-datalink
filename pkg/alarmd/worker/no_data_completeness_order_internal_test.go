// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
)

// One Plan's round is incomplete when either of its bindings is, whichever
// order they come in.
//
// decomposition section 5.3 A1 and section 1.1: absence is judged only when
// the round saw the whole period, and any binding short of FULL - PARTIAL or
// UNAVAILABLE - means it did not. A Plan reads more than one binding when it
// has more than one requirement, and the bindings arrive in an order the Slot
// sorts by requirement name, which says nothing about which one came back
// short. So every pair is run in both orders: a reading that let the binding
// read last decide would judge a round whose first binding was short, and its
// silence would be read as absence (section 3 (d)).
//
// The memory holds a group that is absent this round with its absence open,
// so a round judged by mistake has something to say: an anomaly series and a
// write. An incomplete round says nothing and writes nothing.
func TestOnePlansNoDataRoundIsIncompleteWhenEitherBindingIs(t *testing.T) {
	const (
		full        = execution.CompletenessFull
		partial     = execution.CompletenessPartial
		unavailable = execution.CompletenessUnavailable
	)
	for _, pair := range []struct {
		first, second execution.Completeness
		judged        bool
	}{
		{first: full, second: full, judged: true},
		{first: full, second: partial},
		{first: partial, second: full},
		{first: full, second: unavailable},
		{first: unavailable, second: full},
		{first: partial, second: unavailable},
		{first: unavailable, second: partial},
	} {
		t.Run(string(pair.first)+"_then_"+string(pair.second), func(t *testing.T) {
			due := noDataWiredPlan(t)
			evaluation := int64(noDataPreflightContract(t, []execution.DuePlan{due}).Slot.EvaluationTime)
			store := &horizonNoDataStore{
				groups: []execution.NoDataGroupMemory{{
					GroupKey: "bk_target_cloud_id=0,bk_target_ip=192.0.2.9,__NO_DATA_DIMENSION__=true",
					LastSeen: evaluation - 180, FirstAbsent: evaluation - 120,
				}},
				present: evaluation - 180,
			}
			stream := noDataWiredStream(t, due, store)
			stream.bindings = []execution.NamedInputBinding{
				{Consumer: execution.ConsumerRef{Plan: due.Identity, LevelID: 5, HasLevel: true},
					RequirementID: "a_primary", Completeness: pair.first},
				{Consumer: execution.ConsumerRef{Plan: due.Identity, LevelID: 5, HasLevel: true},
					RequirementID: "b_secondary", Completeness: pair.second},
			}
			if err := stream.loadNoDataMemory(context.Background()); err != nil {
				t.Fatal(err)
			}
			round, err := stream.noDataRoundFor(due, nil, stream.noDataCompleteness(due))
			if err != nil {
				t.Fatal(err)
			}
			if pair.judged {
				if round.outcome != nodata.OutcomeEvaluated || len(round.series) != 1 || round.mutation == nil {
					t.Fatalf("fixture: two FULL bindings gave outcome %q, %d series, mutation %v; want the round "+
						"judged, the open absence reported and the memory written", round.outcome, len(round.series), round.mutation != nil)
				}
				return
			}
			if round.outcome != "SKIPPED_QUERY_NOT_FULL" {
				t.Fatalf("outcome = %q, want SKIPPED_QUERY_NOT_FULL: the Plan's bindings came back %s then %s",
					round.outcome, pair.first, pair.second)
			}
			if len(round.series) != 0 || round.mutation != nil {
				t.Fatalf("an incomplete round produced %d series and mutation %v; it judges and writes nothing",
					len(round.series), round.mutation != nil)
			}
		})
	}
}
