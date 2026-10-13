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
	"encoding/json"
	"strconv"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A delta carries the changed Query Groups sorted, however many there are,
// unless they are every Query Group active after the activation; then it says
// full and carries none, which a Worker reads as "drop everything".
func TestActivationDeltaSaysFullOnlyWhenItCoversTheActiveSet(t *testing.T) {
	identities := func(prefix string, count int) []execution.QueryGroupIdentity {
		out := make([]execution.QueryGroupIdentity, count)
		for index := range out {
			out[index] = execution.QueryGroupIdentity(prefix + strconv.Itoa(index))
		}
		return out
	}
	set := func(groups ...[]execution.QueryGroupIdentity) map[execution.QueryGroupIdentity]struct{} {
		out := map[execution.QueryGroupIdentity]struct{}{}
		for _, group := range groups {
			for _, identity := range group {
				out[identity] = struct{}{}
			}
		}
		return out
	}
	decode := func(t *testing.T, payload []byte) activationDelta {
		t.Helper()
		var delta activationDelta
		if err := json.Unmarshal(payload, &delta); err != nil {
			t.Fatal(err)
		}
		return delta
	}

	payload, err := encodeActivationDelta(7, []execution.QueryGroupIdentity{"qg-b", "qg-a"}, set([]execution.QueryGroupIdentity{"qg-a", "qg-b", "qg-c"}))
	if err != nil {
		t.Fatal(err)
	}
	if delta := decode(t, payload); delta.Full || delta.RecordRevision != 7 || len(delta.QueryGroups) != 2 || delta.QueryGroups[0] != "qg-a" {
		t.Fatalf("small delta = %+v, want the two Query Groups sorted", delta)
	}

	// Past the 2000 the delta used to stop at, on a fleet larger still: a
	// list, since the Query Groups it does not name keep their timelines.
	changed, unchanged := identities("qg-changed-", 2500), identities("qg-kept-", 600)
	payload, err = encodeActivationDelta(8, changed, set(changed, unchanged))
	if err != nil {
		t.Fatal(err)
	}
	if delta := decode(t, payload); delta.Full || len(delta.QueryGroups) != len(changed) {
		t.Fatalf("a delta of %d on an active set of %d = full %v with %d listed, want all %d listed",
			len(changed), len(changed)+len(unchanged), delta.Full, len(delta.QueryGroups), len(changed))
	}

	// Every active Query Group changed, some retired besides: full.
	retired := identities("qg-retired-", 3)
	payload, err = encodeActivationDelta(9, append(append([]execution.QueryGroupIdentity(nil), changed...), retired...), set(changed))
	if err != nil {
		t.Fatal(err)
	}
	if delta := decode(t, payload); !delta.Full || len(delta.QueryGroups) != 0 || len(payload) > 200 {
		t.Fatalf("a delta covering the active set = %+v (%d bytes), want full with no list", delta, len(payload))
	}

	// As many changed as are active, but not the same ones: a list.
	payload, err = encodeActivationDelta(10, changed, set(changed[1:], unchanged[:1]))
	if err != nil {
		t.Fatal(err)
	}
	if delta := decode(t, payload); delta.Full {
		t.Fatalf("a delta missing one active Query Group said full")
	}
	if revision, ok := activationHeaderRevision("12|snapshot@3|-"); !ok || revision != 12 {
		t.Fatalf("header revision = (%d, %v), want 12", revision, ok)
	}
	if _, ok := activationHeaderRevision("snapshot@3|-"); ok {
		t.Fatal("a header without a revision was read as one")
	}
}
