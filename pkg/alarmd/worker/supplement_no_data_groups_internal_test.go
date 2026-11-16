// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

// readRecordingNoDataStore is the real store with every read of the groups
// recorded: a whole load, a scan, and each read by name with its keys.
type readRecordingNoDataStore struct {
	*state.ExecutionStore
	wholeLoads, scans int
	named             [][]string
}

func (store *readRecordingNoDataStore) LoadNoData(ctx context.Context, request execution.NoDataLoadRequest) (execution.NoDataLoadResult, error) {
	store.wholeLoads++
	return store.ExecutionStore.LoadNoData(ctx, request)
}

func (store *readRecordingNoDataStore) ScanNoDataGroups(ctx context.Context, request execution.NoDataScanRequest) (execution.NoDataScanPage, error) {
	store.scans++
	return store.ExecutionStore.ScanNoDataGroups(ctx, request)
}

func (store *readRecordingNoDataStore) ReadNoDataGroups(ctx context.Context, request execution.NoDataGroupsRequest) (execution.NoDataGroupsResult, error) {
	store.named = append(store.named, append([]string(nil), request.Keys...))
	return store.ExecutionStore.ReadNoDataGroups(ctx, request)
}

// A supplement reads its series' groups of the no-data memory by name and
// never the memory whole, and decides each series as the whole read did.
// The memory is the worker's own rounds' against a real store: four hosts
// report, then two stop, then a third. For every Slot of those rounds and the
// one after, a late series of each host, of a host never seen and of one
// no-data does not track is judged twice - over the whole load, as the
// supplement read it before, and through the supplement's own reads - and
// the two agree; the store saw no whole load and no scan, and every read by
// name asked for the supplement's groups and no others.
func TestASupplementReadsItsOwnNoDataGroupsByNameAndDecidesAsTheWholeRead(t *testing.T) {
	address, _ := startLifetimeRedis(t)
	store := openLifetimeStore(t, address)
	due := noDataWiredPlan(t)
	rounds := &storeRounds{t: t, store: store, due: due}
	start := int64(noDataPreflightContract(t, []execution.DuePlan{due}).Slot.EvaluationTime)
	hosts := []map[string]string{massExpiryHost(0), massExpiryHost(1), massExpiryHost(2), massExpiryHost(3)}
	rounds.run(start, hosts)
	rounds.run(start+60, hosts[:2])
	rounds.run(start+120, hosts[:1])

	late := append(append([]map[string]string(nil), hosts...), massExpiryHost(9), map[string]string{"host": "a"})
	whole := nodata.WholeItemGroup().Key()
	answers := map[noDataStanding]int{}
	for _, at := range []int64{start, start + 60, start + 120, start + 180} {
		var series []preparedSeries
		ownKeys := map[string]struct{}{whole: {}}
		for _, dimensions := range late {
			prepared := supplementSeries(t, due, at, dimensions)
			series = append(series, prepared)
			if group, tracked := nodata.Project(dimensions, due.CompiledPlan.NoData().AggDimension); tracked {
				ownKeys[group.Key()] = struct{}{}
			}
		}
		// Before: the whole memory, as the supplement loaded it.
		before := noDataWiredStream(t, due, store)
		before.header.Contract.Slot.EvaluationTime = execution.EvaluationTime(at)
		items, err := noDataPreflightForHeader(before.header)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := store.LoadNoData(context.Background(), execution.NoDataLoadRequest{Contract: before.header.Contract, Items: items})
		if err != nil {
			t.Fatal(err)
		}
		before.noData = loaded

		recording := &readRecordingNoDataStore{ExecutionStore: store}
		stream := noDataWiredStream(t, due, recording)
		stream.header.Contract.Slot.EvaluationTime = execution.EvaluationTime(at)
		stream.request = execution.SlotExecutionRequest{Contract: stream.header.Contract, Operation: execution.OperationSupplement}
		stream.supplement = newSupplementRun(execution.SupplementScope{Series: []execution.SeriesIdentityDigest{series[0].identity}})
		if err := stream.loadNoDataMemory(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := stream.readSupplementNoDataGroups(context.Background(), series); err != nil {
			t.Fatal(err)
		}
		for index, prepared := range series {
			want, got := before.noDataStandingAt(prepared), stream.noDataStandingAt(prepared)
			if got != want {
				t.Fatalf("Slot %d, series %v: standing %d through the supplement's reads, %d over the whole load",
					at, late[index], got, want)
			}
			answers[got]++
		}
		if recording.wholeLoads != 0 || recording.scans != 0 {
			t.Fatalf("Slot %d: the supplement read the memory whole %d times and scanned it %d times",
				at, recording.wholeLoads, recording.scans)
		}
		if len(recording.named) == 0 {
			t.Fatalf("Slot %d: the supplement read none of its groups", at)
		}
		for _, keys := range recording.named {
			for _, key := range keys {
				if _, own := ownKeys[key]; !own {
					t.Fatalf("Slot %d: the supplement read group %q, not one of its series'", at, key)
				}
			}
		}
	}
	// Both answers occur, so agreeing is not agreeing on nothing.
	if answers[noDataRecorded] == 0 || answers[noDataNone] == 0 {
		t.Fatalf("answers %v, want series recorded absent and series not", answers)
	}
	if answers[noDataUnknown] != 0 {
		t.Fatalf("%d series were judged on a memory that could not be read", answers[noDataUnknown])
	}
}
