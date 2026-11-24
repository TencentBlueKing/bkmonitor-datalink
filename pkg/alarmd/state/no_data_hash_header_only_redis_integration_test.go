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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// recordingBackend is a real backend that keeps every hash delta it was asked
// to apply: what the script was handed to HSET and HDEL.
type recordingBackend struct {
	*RedisBackend
	writes []HashDeltaWrite
}

func (backend *recordingBackend) ApplyHashDelta(
	ctx context.Context, write HashDeltaWrite,
) (HashDeltaOutcome, error) {
	backend.writes = append(backend.writes, write)
	return backend.RedisBackend.ApplyHashDelta(ctx, write)
}

// The roster version's upgrade from v1 to v2 (nodata, rosterVersionDomain) on
// a real server: a record whose groups did not move and whose header states
// the old version is rewritten as its header alone. The script is handed no
// group field to set or remove, every group field is byte for byte what it
// was, and the header names the new version and the memory digest that goes
// with it.
//
// The two versions are stand-ins. The store compares the roster version as a
// string and never derives one, so which derivation produced it is not
// visible here; the nodata test of the same round
// (TestTheFirstRoundAfterTheUpgradeRewritesOnlyTheHeader) uses the real ones.
func TestARosterVersionUpgradeWritesOnlyTheHeaderOnARealServer(t *testing.T) {
	const v1, v2 = "roster-v1-stand-in", "roster-v2-stand-in"
	server := startNoDataServer(t)
	recorder := &recordingBackend{RedisBackend: server.backend}
	store := noDataStoreOver(t, recorder)

	first := noDataMutationFrom(t, execution.PlanNoDataMemoryUpdate{
		DerivedFrom: execution.NoDataRepresentationNone,
		Identity:    noDataIdentityV2(), ApplyVersion: coexistenceApplyVersion(0),
		ScheduleRevision: "plan-r1", RosterVersion: v1,
		PresentAsOf: noDataPresentAsOf, Memory: []execution.NoDataGroupMemory{absentGroup("a"), absentGroup("b")},
	})
	if got := storeNoData(t, store, first); got.Status != execution.NoDataApplied {
		t.Fatalf("first write = %+v, want it applied", got)
	}
	before := server.record(t)
	loaded := loadOneNoDataMemory(t, store)
	if loaded.Status != execution.NoDataMemoryFound || loaded.RosterVersion != v1 {
		t.Fatalf("read back = %+v, want the record stating %q", loaded, v1)
	}

	upgraded := noDataMutationFrom(t, execution.PlanNoDataMemoryUpdate{
		DerivedFrom: execution.NoDataRepresentationPerGroup, LoadedApplyVersion: coexistenceApplyVersion(0),
		Identity: noDataIdentityV2(), ExpectedMarkerRevision: loaded.MarkerRevision,
		ApplyVersion: coexistenceApplyVersion(1), ScheduleRevision: "plan-r1", RosterVersion: v2,
		PresentAsOf: loaded.PresentAsOf, Memory: loaded.Groups,
		Loaded: loaded.Groups, LoadedPresentAsOf: loaded.PresentAsOf,
	})
	if len(upgraded.Set) != 0 || len(upgraded.Del) != 0 || upgraded.MemoryDigest == first.MemoryDigest {
		t.Fatalf("setup: the upgrade states set %+v, del %v, digest %s (was %s); want an empty delta and a new digest",
			upgraded.Set, upgraded.Del, upgraded.MemoryDigest, first.MemoryDigest)
	}
	recorder.writes = nil
	if got := storeNoData(t, store, upgraded); got.Status != execution.NoDataApplied {
		t.Fatalf("upgrade write = %+v, want it applied", got)
	}
	if len(recorder.writes) != 1 {
		t.Fatalf("the upgrade reached the script %d times, want once", len(recorder.writes))
	}
	if write := recorder.writes[0]; write.Replace || len(write.Set) != 0 || len(write.Del) != 0 {
		t.Fatalf("the script was handed replace %v, set %d fields, del %v; want the header alone",
			write.Replace, len(write.Set), write.Del)
	}

	after := server.record(t)
	if len(after) != len(before) {
		t.Fatalf("the record holds %d fields after the upgrade, %d before", len(after), len(before))
	}
	for field, value := range before {
		if field == noDataHeaderField {
			continue
		}
		if after[field] != value {
			t.Fatalf("group field %q moved from %q to %q", field, value, after[field])
		}
	}
	header := server.header(t)
	if header.RosterVersion != v2 || header.MarkerRevision != loaded.MarkerRevision+1 {
		t.Fatalf("header = %+v, want roster version %q at revision %d", header, v2, loaded.MarkerRevision+1)
	}
	if reread := loadOneNoDataMemory(t, store); reread.RosterVersion != v2 || len(reread.Groups) != len(loaded.Groups) {
		t.Fatalf("read back after the upgrade = %+v, want %q over the same %d groups", reread, v2, len(loaded.Groups))
	}
}
