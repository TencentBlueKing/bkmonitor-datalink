// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// writtenRoster writes a whole memory of count groups, every third absent,
// and returns the groups as a whole load reads them.
func writtenRoster(t *testing.T, store *ExecutionStore, count int) []execution.NoDataGroupMemory {
	t.Helper()
	groups := make([]execution.NoDataGroupMemory, 0, count)
	for index := range count {
		group := execution.NoDataGroupMemory{GroupKey: fmt.Sprintf("group-%04d", index), LastSeen: noDataPresentAsOf}
		if index%3 == 0 {
			group = execution.NoDataGroupMemory{GroupKey: group.GroupKey, LastSeen: 700, FirstAbsent: 940}
		}
		groups = append(groups, group)
	}
	applied, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{
		Retention: planRetention(retentionEvery(5, time.Minute)), Contract: frozenRef(),
		Items: []execution.PlanNoDataMutation{noDataMutationV2(t, 0, groups...)},
	})
	if err != nil || applied.Items[0].Status != execution.NoDataApplied {
		t.Fatalf("write = %+v, %v", applied, err)
	}
	return groups
}

// The header read carries everything a whole load does but the groups, and
// reads one field: HGET, then the closes on their own key.
func TestANoDataHeaderReadIsTheWholeLoadWithoutItsGroups(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	store := generationStore(t, backend)
	ctx := context.Background()
	writtenRoster(t, store, 12)
	request := execution.NoDataLoadRequest{Contract: frozenRef(), Items: []execution.PlanNoDataLoadItem{noDataLoadItemV2()}}

	whole, err := store.LoadNoData(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	backend.commands = nil
	headers, err := store.ReadNoDataHeaders(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	want := whole.Items[0]
	want.Groups = nil
	if got := headers.Items[0]; !reflect.DeepEqual(got, want) {
		t.Fatalf("header read = %+v\nwant the whole load without its groups %+v", got, want)
	}
	for _, command := range backend.commands {
		if command == "HGETALL" || command == "HSCAN" {
			t.Fatalf("the header read read the groups: %v", backend.commands)
		}
	}
	if len(backend.commands) == 0 || backend.commands[0] != "HGET" {
		t.Fatalf("commands = %v, want the header read with HGET", backend.commands)
	}
	rechecked, err := store.RecheckNoDataHeader(ctx, noDataIdentityV2())
	if err != nil {
		t.Fatal(err)
	}
	if rechecked.MarkerRevision != want.MarkerRevision || rechecked.PersistedMutationDigest != want.PersistedMutationDigest {
		t.Fatalf("recheck = %+v, want the header read again", rechecked)
	}
}

// A scan in segments of a few fields reads every group once its repeats are
// set aside, no command returns more than one segment and its repeat, and the
// groups read by name are the stored ones.
func TestANoDataScanReadsEveryGroupOneSegmentACommand(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte), scanRepeat: true}
	store := generationStore(t, backend)
	ctx := context.Background()
	want := writtenRoster(t, store, 25)
	const count = 4

	backend.replyFields = nil
	seen := map[string]execution.NoDataGroupMemory{}
	repeats := 0
	for cursor := uint64(0); ; {
		page, err := store.ScanNoDataGroups(ctx, execution.NoDataScanRequest{
			Identity: noDataIdentityV2(), Cursor: cursor, Count: count, PresentAsOf: noDataPresentAsOf})
		if err != nil || page.Status != execution.NoDataMemoryFound {
			t.Fatalf("page = %+v, %v", page, err)
		}
		for _, group := range page.Groups {
			if _, again := seen[group.GroupKey]; again {
				repeats++
			}
			seen[group.GroupKey] = group
		}
		if page.Next == 0 {
			break
		}
		cursor = page.Next
	}
	got := make([]execution.NoDataGroupMemory, 0, len(seen))
	for _, group := range seen {
		got = append(got, group)
	}
	sort.Slice(got, func(left, right int) bool { return got[left].GroupKey < got[right].GroupKey })
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scanned %+v\nwant %+v", got, want)
	}
	if repeats == 0 {
		t.Fatal("the scan returned no field twice; the fixture is not exercising the repeat")
	}
	for _, fields := range backend.replyFields {
		if fields > count+1 {
			t.Fatalf("a scan command returned %d fields, more than a segment of %d and its repeat", fields, count)
		}
	}

	keys := []string{want[3].GroupKey, "group-missing", want[0].GroupKey}
	read, err := store.ReadNoDataGroups(ctx, execution.NoDataGroupsRequest{
		Identity: noDataIdentityV2(), Keys: keys, PresentAsOf: noDataPresentAsOf})
	if err != nil || read.Status != execution.NoDataMemoryFound {
		t.Fatalf("read = %+v, %v", read, err)
	}
	if !reflect.DeepEqual(read.Groups, []execution.NoDataGroupMemory{want[3], {}, want[0]}) {
		t.Fatalf("read by name = %+v, want the two stored groups and an empty one for the missing", read.Groups)
	}
}

// A group field that is not a group, or one that does not decode, is a
// corrupt page rather than a group left out.
func TestANoDataScanCallsAnUndecodableFieldCorrupt(t *testing.T) {
	for name, field := range map[string][2]string{
		"not a group": {"stray", "p"},
		"not json":    {noDataGroupPrefix + "broken", "{"},
	} {
		t.Run(name, func(t *testing.T) {
			backend := &casMemoryBackend{values: make(map[string][]byte)}
			store := generationStore(t, backend)
			writtenRoster(t, store, 3)
			backend.hashes[noDataHashKey(t)][field[0]] = []byte(field[1])
			page, err := store.ScanNoDataGroups(context.Background(), execution.NoDataScanRequest{
				Identity: noDataIdentityV2(), Count: 100, PresentAsOf: noDataPresentAsOf})
			if err != nil || page.Status != execution.NoDataMemoryTerminal {
				t.Fatalf("page = %+v, %v; want it terminal", page, err)
			}
		})
	}
}
