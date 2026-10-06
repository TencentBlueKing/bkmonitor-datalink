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

// A delta carries the changed Query Groups sorted, up to the bound; past it
// it says full and carries none, which a Worker reads as "drop everything".
func TestActivationDeltaSaysFullPastItsBound(t *testing.T) {
	small := []execution.QueryGroupIdentity{"qg-b", "qg-a"}
	payload, err := encodeActivationDelta(7, small)
	if err != nil {
		t.Fatal(err)
	}
	var delta activationDelta
	if err := json.Unmarshal(payload, &delta); err != nil {
		t.Fatal(err)
	}
	if delta.Full || delta.RecordRevision != 7 || len(delta.QueryGroups) != 2 || delta.QueryGroups[0] != "qg-a" {
		t.Fatalf("small delta = %+v, want the two Query Groups sorted", delta)
	}
	large := make([]execution.QueryGroupIdentity, activationDeltaMaxQueryGroups+1)
	for index := range large {
		large[index] = execution.QueryGroupIdentity("qg-" + strconv.Itoa(index))
	}
	payload, err = encodeActivationDelta(8, large)
	if err != nil {
		t.Fatal(err)
	}
	var full activationDelta
	if err := json.Unmarshal(payload, &full); err != nil {
		t.Fatal(err)
	}
	if !full.Full || len(full.QueryGroups) != 0 || len(payload) > 200 {
		t.Fatalf("large delta = %+v (%d bytes), want full with no list", full, len(payload))
	}
	if revision, ok := activationHeaderRevision("12|snapshot@3|-"); !ok || revision != 12 {
		t.Fatalf("header revision = (%d, %v), want 12", revision, ok)
	}
	if _, ok := activationHeaderRevision("snapshot@3|-"); ok {
		t.Fatal("a header without a revision was read as one")
	}
}
