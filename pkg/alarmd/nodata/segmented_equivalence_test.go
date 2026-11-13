// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// segmentedOutcome is what a segmented round said, gathered for comparison
// with the whole read: every series in key order and the statement.
type segmentedOutcome struct {
	result SegmentedResult
	series []SyntheticSeries
}

// runSegmented feeds a round the way the worker does: the present groups the
// scan returns, in any order and with repeats; the settling; the absent names
// once each, in any order; then the finish.
func runSegmented(input PlanSlotInput, random *rand.Rand) (segmentedOutcome, error) {
	set := NewPresentSet(input.NoData.AggDimension)
	for _, series := range input.Series {
		set.Add(series)
	}
	segmented := input
	segmented.Series, segmented.Present = nil, set
	segmented.Snapshot.Groups = nil
	round, err := NewSegmentedRound(segmented)
	if err != nil {
		return segmentedOutcome{}, err
	}
	var absent []execution.NoDataGroupMemory
	names := map[string]bool{}
	for _, group := range shuffledWithRepeats(input.Snapshot.Groups, random) {
		if round.Present(group.GroupKey) {
			if _, _, err := round.Feed(group); err != nil {
				return segmentedOutcome{}, err
			}
			continue
		}
		if !names[group.GroupKey] {
			names[group.GroupKey] = true
			absent = append(absent, group)
		}
	}
	round.Settle()
	random.Shuffle(len(absent), func(left, right int) { absent[left], absent[right] = absent[right], absent[left] })
	var said []GroupJudgement
	for _, group := range absent {
		judgement, judged, err := round.Feed(group)
		if err != nil {
			return segmentedOutcome{}, err
		}
		if judged && judgement.Verdict != "" && judgement.Verdict != VerdictUnavailable {
			said = append(said, judgement)
		}
	}
	result, err := round.Finish(input.ApplyVersion)
	if err != nil {
		return segmentedOutcome{}, err
	}
	outcome := segmentedOutcome{result: result}
	if !result.Judged {
		if len(said) != 0 {
			return segmentedOutcome{}, fmt.Errorf("a round not judged said %d absences while it was fed", len(said))
		}
		return outcome, nil
	}
	for _, part := range [][]GroupJudgement{said, result.Rest} {
		series, err := round.Synthesize(part)
		if err != nil {
			return segmentedOutcome{}, err
		}
		outcome.series = append(outcome.series, series...)
	}
	present, err := round.SynthesizePresent(result.PresentKeys)
	if err != nil {
		return segmentedOutcome{}, err
	}
	outcome.series = append(outcome.series, present...)
	sort.SliceStable(outcome.series, func(left, right int) bool {
		return outcome.series[left].Group.Key() < outcome.series[right].Group.Key()
	})
	return outcome, nil
}

// B2 (plan 5.4): a round over a memory read in segments - the present groups
// scanned in any order and with repeats, the absent ones read by name once
// each in any order - says what the whole read says: the outcome, the counts,
// every series, and the statement, digest for digest. Both rounds of a case
// are checked, the second against the record the first wrote.
func TestASegmentedRoundMatchesTheWholeRead(t *testing.T) {
	named := namedEquivalenceCases(t)
	written, judged := 0, 0
	for seed := 0; seed < equivalenceCases+len(named); seed++ {
		random := rand.New(rand.NewSource(int64(seed) + 7919))
		var input PlanSlotInput
		label := fmt.Sprintf("seed %d", seed)
		if seed < len(named) {
			input, label = named[seed].input, named[seed].name
		} else {
			input = equivalenceCase(t, random)
		}
		for pass := 0; pass < 2; pass++ {
			name := fmt.Sprintf("%s round %d", label, pass)
			want, wantErr := EvaluatePlanSlot(input)
			got, err := runSegmented(input, random)
			if (err != nil) != (wantErr != nil) {
				t.Fatalf("%s: segmented error %v, whole read error %v", name, err, wantErr)
			}
			if wantErr != nil {
				break
			}
			if got.result.Outcome != want.Outcome || !reflect.DeepEqual(got.result.Facts, want.Facts) {
				t.Fatalf("%s: segmented %s %+v, whole read %s %+v", name, got.result.Outcome, got.result.Facts,
					want.Outcome, want.Facts)
			}
			if len(got.series) != len(want.Series) || (len(want.Series) != 0 && !reflect.DeepEqual(got.series, want.Series)) {
				t.Fatalf("%s: segmented series %+v\nwhole read %+v", name, got.series, want.Series)
			}
			if !reflect.DeepEqual(got.result.Mutation, want.Mutation) {
				t.Fatalf("%s: segmented statement %+v\nwhole read %+v", name, got.result.Mutation, want.Mutation)
			}
			if got.result.Judged {
				judged++
			}
			if want.Mutation == nil {
				break
			}
			written++
			input.Snapshot = nextRoundSnapshot(input.Snapshot, want.Mutation)
		}
	}
	if written == 0 || judged == 0 {
		t.Fatalf("judged %d and wrote %d rounds; the comparison never reached a statement", judged, written)
	}
}

// A small-scale stand-in for 500,000 groups: thousands of history groups, a
// third of them gone, scanned in segments of a few fields with repeats. The
// statement is the whole read's, and the round holds no delta entry for a
// group whose stored value did not move.
func TestASegmentedRoundOverThousandsOfGroupsMatchesTheWholeRead(t *testing.T) {
	const groups = 3000
	var stored []execution.NoDataGroupMemory
	var series []map[string]string
	for index := range groups {
		ip := fmt.Sprintf("10.%d.%d.%d", index/65536, index/256%256, index%256)
		key := hostTargetGroup(HostIdentity{IP: ip, CloudID: "0"}).Key()
		stored = append(stored, execution.NoDataGroupMemory{GroupKey: key, LastSeen: 940})
		if index%3 != 0 {
			series = append(series, map[string]string{HostIPDimension: ip, HostCloudDimension: "0"})
		}
	}
	sort.Slice(stored, func(left, right int) bool { return stored[left].GroupKey < stored[right].GroupKey })
	input := historyPlanSlotInput(storedSnapshot(t, 940, stored...))
	input.Series = series
	want, err := EvaluatePlanSlot(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := runSegmented(input, rand.New(rand.NewSource(1)))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.result.Mutation, want.Mutation) || !reflect.DeepEqual(got.series, want.Series) {
		t.Fatalf("segmented statement %+v\nwhole read %+v", got.result.Mutation, want.Mutation)
	}
	if want.Mutation == nil || len(want.Mutation.Set) != groups/3 || len(want.Mutation.Del) != 0 {
		t.Fatalf("whole read statement %+v, want the %d groups gone set and nothing else", want.Mutation, groups/3)
	}
}
