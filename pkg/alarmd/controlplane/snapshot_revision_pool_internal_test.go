// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"bytes"
	"encoding/json"
	"fmt"
	"runtime"
	"runtime/metrics"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/racedetector"
)

// The group-at-a-time encoding writes the bytes the one-call encoding wrote,
// including for no groups at all and for the characters the encoder escapes
// or, with HTML escaping off, does not.
func TestQueryGroupsEncodeOneAtATimeToTheBytesOfOneCall(t *testing.T) {
	odd := "a<b>&c d e\"f\\g"
	cases := map[string][]QueryGroup{
		"nil":   nil,
		"empty": {},
		"one":   {{Identity: execution.QueryGroupIdentity("qg-" + odd), MembershipDigest: odd}},
		"three": {
			{Identity: "qg-1", MembershipDigest: "m1", Plans: []FrozenPlan{{PlanRevision: odd}}},
			{Identity: "qg-2", ScheduleRevision: execution.ScheduleRevision(odd)},
			{Identity: "qg-3", Plans: []FrozenPlan{{PlanRevision: "r"}, {PlanRevision: "s", StateGeneration: "g2"}}},
		},
	}
	for name, groups := range cases {
		var whole bytes.Buffer
		encoder := json.NewEncoder(&whole)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(groups); err != nil {
			t.Fatal(err)
		}
		got, err := appendQueryGroupsJSON(nil, groups)
		if err != nil {
			t.Fatal(err)
		}
		if want := bytes.TrimSuffix(whole.Bytes(), []byte{'\n'}); !bytes.Equal(got, want) {
			t.Fatalf("%s: one at a time wrote\n%s\none call wrote\n%s", name, got, want)
		}
		_, declinedBefore := contract.CanonicalStreamCounts()
		revision, err := deriveSnapshotRevision(groups)
		if err != nil {
			t.Fatal(err)
		}
		// The single-pass canonical form answered. A decline would hand the
		// whole catalog to the established path, which encodes it in one
		// call and puts the catalog-sized buffer back in the pool.
		if _, declinedAfter := contract.CanonicalStreamCounts(); declinedAfter != declinedBefore {
			t.Fatalf("%s: the single-pass canonical form declined the catalog (%d -> %d)", name, declinedBefore, declinedAfter)
		}
		want, err := contract.DeriveCanonicalDigestV2("alarmd-strategy-snapshot-v1", groups)
		if err != nil {
			t.Fatal(err)
		}
		if revision != execution.SnapshotRevision(want) {
			t.Fatalf("%s: revision %s, want %s", name, revision, want)
		}
	}
}

// What deriving a revision leaves live once it returns, on one P that keeps
// encoding JSON as a working replica does. The one-call digest is the other
// branch: it leaves the catalog's whole encoding in encoding/json's pooled
// encode state, which this measurement sees, so a revision that went back to
// one call would fail here. The bound is a fraction of the catalog's own
// encoded size, not a heap figure.
func TestDerivingTheSnapshotRevisionLeavesNoCatalogSizedBufferBehind(t *testing.T) {
	if racedetector.Enabled {
		t.Skip("under -race sync.Pool drops a share of what is put back, so the pooled buffer is not reliably kept")
	}
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	groups := make([]QueryGroup, 4000)
	for index := range groups {
		groups[index] = QueryGroup{Identity: execution.QueryGroupIdentity(fmt.Sprintf("qg-%d", index)),
			MembershipDigest: strings.Repeat("m", 4096)}
	}
	encoded, err := appendQueryGroupsJSON(nil, groups)
	if err != nil {
		t.Fatal(err)
	}
	size := uint64(len(encoded))
	encoded = nil

	retained := func(derive func() error) uint64 {
		before := drainedLiveHeap()
		if err := derive(); err != nil {
			t.Fatal(err)
		}
		after := settledLiveHeap()
		if after < before {
			return 0
		}
		return after - before
	}
	_, declinedBefore := contract.CanonicalStreamCounts()
	unpooled := retained(func() error { _, err := deriveSnapshotRevision(groups); return err })
	if _, declinedAfter := contract.CanonicalStreamCounts(); declinedAfter != declinedBefore {
		t.Fatalf("the single-pass canonical form declined the catalog (%d -> %d): the established path encodes it in one call", declinedBefore, declinedAfter)
	}
	oneCall := retained(func() error {
		_, err := contract.DeriveCanonicalDigestV2("alarmd-strategy-snapshot-v1", groups)
		return err
	})
	runtime.KeepAlive(groups)
	t.Logf("a %d byte encoding left live: %d bytes one group at a time, %d bytes in one call", size, unpooled, oneCall)
	if oneCall < size/2 {
		t.Fatalf("setup: the one-call digest left %d bytes live of a %d byte encoding; the measurement cannot see the pooled buffer", oneCall, size)
	}
	if unpooled >= size/4 {
		t.Fatalf("deriving the revision left %d bytes live of a %d byte encoding; the one-call digest left %d", unpooled, size, oneCall)
	}
}

// drainedLiveHeap is the live heap once encoding/json's pool is empty: three
// collections with no JSON between them drop whatever an earlier test or an
// earlier run of this one left there, since a pooled entry nobody takes is
// gone after two.
func drainedLiveHeap() uint64 {
	for range 3 {
		runtime.GC()
	}
	return liveHeap()
}

// settledLiveHeap is the live heap after collections between which JSON is
// encoded, so encoding/json's pool stays in use the way it does on a replica
// and what it holds survives the collections.
func settledLiveHeap() uint64 {
	for range 4 {
		_, _ = json.Marshal(QueryGroup{Identity: "settle"})
		runtime.GC()
	}
	return liveHeap()
}

func liveHeap() uint64 {
	sample := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
	metrics.Read(sample)
	return sample[0].Value.Uint64()
}
