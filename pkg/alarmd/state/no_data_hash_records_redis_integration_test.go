// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// presentHostGroups is n groups of a host-level strategy, every one seen in the
// round the header names: the steady state of a large history roster.
func presentHostGroups(n int) []execution.NoDataGroupMemory {
	groups := make([]execution.NoDataGroupMemory, n)
	for index := range groups {
		groups[index] = execution.NoDataGroupMemory{
			GroupKey: fmt.Sprintf("bk_target_cloud_id=0,bk_target_ip=192.0.%d.%d,%s=true",
				index/256, index%256, contract.NoDataDimensionTag),
			LastSeen: noDataPresentAsOf,
		}
	}
	return groups
}

// The default group bound is taken on both sides on a real server: one under
// it and at it are written whole, one over it is refused with both numbers
// and nothing is written.
//
// The bound is decision-008 section 2's derived guard, "of the order of
// 10^5", and decision-018 section 1.3 gives its default, 100000. It bounds the
// memory after the write, and "over" is what is refused, so 100000 groups is a
// memory the store keeps. The store here is opened without a bound of its own,
// so the default is the one in force.
func TestTheDefaultGroupBoundIsTakenOnBothSidesOnARealServer(t *testing.T) {
	const limit = 100000
	server := startNoDataServer(t)
	ctx := context.Background()
	key := noDataHashKey(t)
	for _, arm := range []struct {
		groups  int
		refused bool
	}{
		{groups: limit - 1}, {groups: limit}, {groups: limit + 1, refused: true},
	} {
		t.Run(fmt.Sprintf("%d groups", arm.groups), func(t *testing.T) {
			if err := server.client.FlushAll(ctx).Err(); err != nil {
				t.Fatal(err)
			}
			store := server.store(t)
			got := storeNoData(t, store, firstNoDataWrite(t, 0, presentHostGroups(arm.groups)...))
			if !arm.refused {
				if got.Status != execution.NoDataApplied {
					t.Fatalf("write of %d groups = %+v, want it kept: the bound refuses only what is over it", arm.groups, got)
				}
				if fields := server.client.HLen(ctx, key).Val(); fields != int64(arm.groups+1) {
					t.Fatalf("the record holds %d fields, want %d groups and the header", fields, arm.groups)
				}
				return
			}
			if got.Status != execution.NoDataRejected ||
				got.ReasonCode != execution.ReasonCode(contract.ReasonStateBudgetExceeded) {
				t.Fatalf("write of %d groups = %+v, want a refusal by name", arm.groups, got)
			}
			want := execution.NoDataRecordSize{Record: execution.NoDataRecordGroups, Groups: limit + 1, Limit: limit}
			if got.Size == nil || *got.Size != want {
				t.Fatalf("refusal measured %+v, want %+v", got.Size, want)
			}
			if exists := server.client.Exists(ctx, key).Val(); exists != 0 {
				t.Fatal("a refused write left a record behind")
			}
		})
	}
}

// A record the store cannot read is TERMINAL on a real server: fields that are
// not part of one.
//
// decision-008 section 3: a field that is neither the header nor a group, or a
// group value that is neither present nor an absence, refuses the whole record
// rather than reading the rest of it -- nothing is guessed. The store's word for
// a record no build can read is TERMINAL with STATE_CORRUPT, kept apart from
// UNREADABLE, which is a record a newer build wrote (decision-018 section 1.2).
func TestARecordTheStoreCannotReadIsTerminalOnARealServer(t *testing.T) {
	server := startNoDataServer(t)
	ctx := context.Background()
	corrupt := execution.ReasonCode(contract.ReasonStateCorrupt)
	for name, fields := range map[string]map[string]string{
		"a header that is not JSON":                 {noDataHeaderField: "{"},
		"a field that is neither header nor group":  {"stray": "1"},
		"a group value neither present nor absence": {noDataGroupPrefix + "a": "what"},
		"a group that remembers nothing":            {noDataGroupPrefix + "a": "{}"},
		"a group suppressed before the epoch":       {noDataGroupPrefix + "a": `{"first_absent":940,"suppressed_at":-1}`},
	} {
		t.Run("a per-group record with "+name, func(t *testing.T) {
			if err := server.client.FlushAll(ctx).Err(); err != nil {
				t.Fatal(err)
			}
			record := map[string]interface{}{}
			for field, value := range perGroupRecord(t, 3, applyVersion(), 940, absentGroup("kept")) {
				record[field] = string(value)
			}
			for field, value := range fields {
				record[field] = value
			}
			if err := server.client.HSet(ctx, noDataHashKey(t), record).Err(); err != nil {
				t.Fatal(err)
			}
			snapshot := loadOneNoDataMemory(t, server.store(t))
			if snapshot.Status != execution.NoDataMemoryTerminal || snapshot.ReasonCode != corrupt {
				t.Fatalf("load = %+v, want TERMINAL with %s: the record cannot be read as it is", snapshot, corrupt)
			}
			if len(snapshot.Groups) != 0 {
				t.Fatalf("a terminal load handed back groups %+v", snapshot.Groups)
			}
		})
	}
}

// The two tracking facts are written through a real server and read back from
// it: a group's suppressed-at in that group's own field, and the Plan's
// tracking-exhausted-at in the header.
//
// decision-018 section 1.1 places them there, and section 2 item 1 is why this
// has to go through the store's own write and a real server: the header is
// built by listing fields in the store, a helper-level round trip agrees with
// itself, and the fact that is dropped by the store's write reads back as
// zero -- an empty roster read as never exhausted, rebuilt next round as a
// whole-item absence.
func TestTheTrackingFactsSurviveTheStoreOnARealServer(t *testing.T) {
	server := startNoDataServer(t)
	store := server.store(t)
	ctx := context.Background()
	key := noDataHashKey(t)

	suppressed := execution.NoDataGroupMemory{GroupKey: "a", FirstAbsent: 940, SuppressedAt: 960}
	if got := storeNoData(t, store, firstNoDataWrite(t, 0, suppressed)); got.Status != execution.NoDataApplied {
		t.Fatalf("first write = %+v", got)
	}
	var stored noDataGroupAbsenceValue
	raw := server.client.HGet(ctx, key, noDataGroupPrefix+"a").Val()
	if err := json.Unmarshal([]byte(raw), &stored); err != nil || stored.SuppressedAt != 960 {
		t.Fatalf("the server holds group a as %q, want suppressed_at 960 in its own field", raw)
	}
	loaded := loadOneNoDataMemory(t, store)
	if len(loaded.Groups) != 1 || loaded.Groups[0] != suppressed || loaded.TrackingExhaustedAt != 0 {
		t.Fatalf("read back %+v exhausted at %d, want the suppressed group and no Plan-level fact",
			loaded.Groups, loaded.TrackingExhaustedAt)
	}

	// The last group goes, and the Plan-level fact takes its place.
	exhausted := noDataMutationFrom(t, execution.PlanNoDataMemoryUpdate{
		DerivedFrom: execution.NoDataRepresentationPerGroup, LoadedApplyVersion: coexistenceApplyVersion(0),
		ExpectedMarkerRevision: loaded.MarkerRevision, Identity: noDataIdentityV2(),
		ApplyVersion: coexistenceApplyVersion(1), ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1",
		PresentAsOf: noDataPresentAsOf, TrackingExhaustedAt: 970,
		Loaded: loaded.Groups, LoadedPresentAsOf: noDataPresentAsOf,
	})
	if got := storeNoData(t, store, exhausted); got.Status != execution.NoDataApplied {
		t.Fatalf("exhausting write = %+v", got)
	}
	if header := server.header(t); header.TrackingExhaustedAt != 970 {
		t.Fatalf("the server's header holds tracking_exhausted_at %d, want 970", header.TrackingExhaustedAt)
	}
	if fields := server.client.HLen(ctx, key).Val(); fields != 1 {
		t.Fatalf("the exhausted record holds %d fields, want the header alone", fields)
	}
	loaded = loadOneNoDataMemory(t, store)
	if loaded.Status != execution.NoDataMemoryFound || loaded.TrackingExhaustedAt != 970 || len(loaded.Groups) != 0 {
		t.Fatalf("read back %+v, want the exhausted roster: found, no groups, exhausted at 970", loaded)
	}
}

// Deleting a Plan's last group leaves a record whose header still reads, and
// an older single-value record beside that emptied record is not read back,
// on a real server - nor once the per-group record is gone.
//
// The no-data tracking proposal section 3: the header stays when every group
// is gone, keeping the version and the idempotence; its section 9 lists
// "emptied leaves the header" and "the old record does not come back" among
// the storage checks.
func TestAnEmptiedRecordKeepsItsHeaderAndHidesAnOlderWholeMemoryOnARealServer(t *testing.T) {
	server := startNoDataServer(t)
	store := server.store(t)
	ctx := context.Background()
	key := noDataHashKey(t)

	if got := storeNoData(t, store, firstNoDataWrite(t, 1, absentGroup("a"), absentGroup("b"))); got.Status != execution.NoDataApplied {
		t.Fatalf("first write = %+v", got)
	}
	loaded := loadOneNoDataMemory(t, store)
	emptied := deltaNoDataWrite(t, 2, loaded.MarkerRevision, 1, loaded.Groups)
	if len(emptied.Del) != 2 || len(emptied.Set) != 0 {
		t.Fatalf("setup: the emptying statement is %+v, want both groups deleted and nothing set", emptied)
	}
	if got := storeNoData(t, store, emptied); got.Status != execution.NoDataApplied {
		t.Fatalf("emptying write = %+v", got)
	}
	if fields := server.record(t); len(fields) != 1 || fields[noDataHeaderField] == "" {
		t.Fatalf("the emptied record holds %v, want the header alone", fields)
	}
	readsEmpty := func(t *testing.T, when string) {
		t.Helper()
		snapshot := loadOneNoDataMemory(t, store)
		if snapshot.Status != execution.NoDataMemoryFound || snapshot.MarkerRevision != 2 || len(snapshot.Groups) != 0 ||
			snapshot.Representation != execution.NoDataRepresentationPerGroup {
			t.Fatalf("%s: load = %+v, want the emptied per-group record: found at revision 2 with no groups", when, snapshot)
		}
	}
	readsEmpty(t, "alone")

	old := legacyWholeMemoryRecord(t, coexistenceApplyVersion(0), absentGroup("from-the-old-record"))
	if err := server.client.Set(ctx, legacyNoDataKey(t), old, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	readsEmpty(t, "beside an older single-value record")

	server.client.Del(ctx, key)
	if snapshot := loadOneNoDataMemory(t, store); snapshot.Status != execution.NoDataMemoryMissing {
		t.Fatalf("with the per-group record gone the load read %+v, want no memory: the older single-value "+
			"record is not read", snapshot)
	}
}

// The bytes a per-group record holds are the bytes decision-008 section 7.2 (a)
// predicts, within its 10%, on a real server: groups times the bytes of one
// group, plus the header.
//
// One group is its field, the prefix and the group key, and its value: one
// byte for a group present in the round the header names, the absence JSON
// otherwise (section 1). The size is the section's own example, a Plan of
// 3,200 groups. What is read is HGETALL's own answer from the server, which
// the section allows in place of MEMORY USAGE; MEMORY USAGE against the same
// formula is a separate question.
func TestANoDataHashHoldsTheBytesTheCapacityFormulaPredictsOnARealServer(t *testing.T) {
	const groups = 3200
	absence, err := json.Marshal(noDataGroupAbsenceValue{LastSeen: 900, FirstAbsent: 940})
	if err != nil {
		t.Fatal(err)
	}
	for _, arm := range []struct {
		name  string
		value int
		shape func(execution.NoDataGroupMemory) execution.NoDataGroupMemory
	}{
		{name: "every group present", value: 1,
			shape: func(group execution.NoDataGroupMemory) execution.NoDataGroupMemory { return group }},
		{name: "every group absent", value: len(absence),
			shape: func(group execution.NoDataGroupMemory) execution.NoDataGroupMemory {
				return execution.NoDataGroupMemory{GroupKey: group.GroupKey, LastSeen: 900, FirstAbsent: 940}
			}},
	} {
		t.Run(arm.name, func(t *testing.T) {
			server := startNoDataServer(t)
			memory := presentHostGroups(groups)
			predicted := 0
			for index, group := range memory {
				memory[index] = arm.shape(group)
				predicted += len(noDataGroupPrefix) + len(group.GroupKey) + arm.value
			}
			if got := storeNoData(t, server.store(t), firstNoDataWrite(t, 0, memory...)); got.Status != execution.NoDataApplied {
				t.Fatalf("write = %+v", got)
			}
			fields := server.record(t)
			predicted += len(noDataHeaderField) + len(fields[noDataHeaderField])
			held := 0
			for name, value := range fields {
				held += len(name) + len(value)
			}
			if deviation := math.Abs(float64(held-predicted)) / float64(predicted); deviation > 0.10 {
				t.Fatalf("the record holds %d bytes, the formula predicts %d: %.1f%% off, past the 10%% decision-008 "+
					"section 7.2 allows", held, predicted, 100*deviation)
			}
			t.Logf("%d groups, %s: HGETALL %d bytes, formula %d", groups, arm.name, held, predicted)
		})
	}
}
