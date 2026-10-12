// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"context"
	"encoding/json"
	"runtime"
	"strconv"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// objectFootprintPlans are the Plan counts per Query Group the charge is
// checked against. Not evenly spaced, for the reason the timeline sweep is
// not: the decoded Plans slice grows by doubling and rounds each growth to a
// size class, and 9 and 18 are teeth an even sweep walks past.
var objectFootprintPlans = []int{1, 2, 3, 5, 9, 14, 18, 19, 40, 80}

// TestDecodedQueryGroupObjectHeapFootprint is the measurement the object
// cache's decoded charge is taken from: the heap a decoded Query Group
// object retains, per copy and worst of three, held across two collections
// each side, against the stored bytes the cache counts it by. Charging less
// than it holds is the failure that matters: the observation memory line
// would leave detection's caches less room than they can take.
func TestDecodedQueryGroupObjectHeapFootprint(t *testing.T) {
	documents := realThresholdDocuments(t)
	for _, plans := range objectFootprintPlans {
		sources := make([]controlplane.SourceStrategy, 0, plans)
		for index := 0; index < plans; index++ {
			var decoded map[string]any
			if err := json.Unmarshal(documents[index%len(documents)], &decoded); err != nil {
				t.Fatal(err)
			}
			id := 5000 + index
			decoded["id"] = float64(id)
			encoded, err := json.Marshal(decoded)
			if err != nil {
				t.Fatal(err)
			}
			sources = append(sources, controlplane.SourceStrategy{SourceID: strconv.Itoa(id), Document: encoded,
				Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}})
		}
		catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: sources,
			Planner: &recordingPlanner{facts: queryFacts(t)}})
		if err != nil {
			t.Fatal(err)
		}
		if len(catalog.QueryGroups) != 1 || len(catalog.QueryGroups[0].Plans) != plans {
			t.Fatalf("setup: %d groups, want one of %d Plans", len(catalog.QueryGroups), plans)
		}
		h := newObjectCatalogHarness(t)
		h.publish(t, catalogWithSchedule(t, catalog, 60, 0))
		digest, err := controlplane.DeriveQueryGroupObjectDigest(catalog.QueryGroups[0])
		if err != nil {
			t.Fatal(err)
		}
		payload, err := h.client.Get(h.ctx, h.prefix+":qgobj:"+string(digest)).Bytes()
		if err != nil {
			t.Fatal(err)
		}
		measured := worstDecodedObjectHeap(t, payload)
		charged := float64(controlplane.DecodedObjectBytesForTest(len(payload)))
		t.Logf("plans=%d payload=%d worst_decoded_heap=%.0f ratio=%.3f charged=%.0f", plans, len(payload), measured,
			measured/float64(len(payload)), charged)
		// The entry's list element, map slot and header ride on the charge.
		if charged < measured+256 {
			t.Fatalf("plans=%d charges %.0f bytes for %.0f bytes of decoded object and its entry", plans, charged, measured+256)
		}
		if charged > 2*measured {
			t.Fatalf("plans=%d charges %.0f bytes for %.0f bytes held", plans, charged, measured)
		}
	}
}

func worstDecodedObjectHeap(t *testing.T, payload []byte) float64 {
	t.Helper()
	const copies = 32
	worst := 0.0
	for round := 0; round < 3; round++ {
		runtime.GC()
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		kept := make([]controlplane.QueryGroupObject, 0, copies)
		for index := 0; index < copies; index++ {
			var object controlplane.QueryGroupObject
			if err := json.Unmarshal(payload, &object); err != nil {
				t.Fatal(err)
			}
			kept = append(kept, object)
		}
		runtime.GC()
		runtime.GC()
		runtime.ReadMemStats(&after)
		runtime.KeepAlive(kept)
		if per := float64(after.HeapAlloc-before.HeapAlloc) / copies; per > worst {
			worst = per
		}
	}
	return worst
}

// The object cache as a budget of observation memory is its working set,
// not its ceiling, charged in decoded bytes: the cache counts stored bytes,
// and the heap its objects take is the objects decoded from them. Nobody
// reading into it, its size is what it holds.
func TestTheObjectCachesBudgetIsChargedDecoded(t *testing.T) {
	h := newObjectCatalogHarness(t)
	if err := h.repository.ConfigureObjectCache(64, 1<<20); err != nil {
		t.Fatal(err)
	}
	if size, held := h.repository.ObjectCacheBudget(); size != 0 || held != 0 {
		t.Fatalf("an empty object cache of 1 MiB nobody reads into = (%d, %d), want (0, 0)", size, held)
	}
	catalog := objectCatalogTwoGroups(t, 80)
	h.publish(t, catalog)
	digest, err := controlplane.DeriveQueryGroupObjectDigest(catalog.QueryGroups[0])
	if err != nil {
		t.Fatal(err)
	}
	payload, err := h.client.Get(h.ctx, h.prefix+":qgobj:"+string(digest)).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.repository.LoadQueryGroupObject(h.ctx, digest); err != nil {
		t.Fatal(err)
	}
	size, held := h.repository.ObjectCacheBudget()
	if want := uint64(controlplane.DecodedObjectBytesForTest(len(payload))); size != want || held != want {
		t.Fatalf("one object of %d stored bytes read = (%d, %d), want (%d, %d) decoded", len(payload), size, held, want, want)
	}
}
