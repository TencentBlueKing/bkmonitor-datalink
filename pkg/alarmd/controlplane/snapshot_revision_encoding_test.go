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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The snapshot revision is the digest over the whole Query Group slice, as it
// was when the slice was handed to the encoder in one call: a catalog built
// from real strategy documents, with their Plans' marshalers, gets the
// revision the one-call digest gives it.
func TestTheSnapshotRevisionIsTheDigestOfTheWholeQueryGroupSlice(t *testing.T) {
	for _, sources := range []string{"A", "AB", "ABC"} {
		catalog := headCatalog(t, 80, sources)
		want, err := contract.DeriveCanonicalDigestV2("alarmd-strategy-snapshot-v1", catalog.QueryGroups)
		if err != nil {
			t.Fatal(err)
		}
		if len(catalog.QueryGroups) != len(sources) || catalog.SnapshotRevision != execution.SnapshotRevision(want) {
			t.Fatalf("sources %s: %d groups, revision %s, want %d groups and %s",
				sources, len(catalog.QueryGroups), catalog.SnapshotRevision, len(sources), want)
		}
	}
}
