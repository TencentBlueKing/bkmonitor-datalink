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
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The object a manifest names for a Query Group is found in the order the
// writer stores the entries, and also in a manifest that is not in it: the
// answer is the one the walk over the entries always gave.
func TestAManifestNamesAQueryGroupsObjectInOrderOrNot(t *testing.T) {
	ordered := CatalogManifest{}
	for index := 0; index < 300; index++ {
		ordered.QueryGroups = append(ordered.QueryGroups, ManifestQueryGroup{
			QueryGroup:   execution.QueryGroupIdentity(fmt.Sprintf("qg-%04d", index)),
			ObjectDigest: execution.ObjectDigest(fmt.Sprintf("object-%04d", index)),
		})
	}
	reversed := CatalogManifest{}
	for index := len(ordered.QueryGroups) - 1; index >= 0; index-- {
		reversed.QueryGroups = append(reversed.QueryGroups, ordered.QueryGroups[index])
	}
	for _, manifest := range []CatalogManifest{ordered, reversed} {
		for _, index := range []int{0, 1, 149, 150, 298, 299} {
			group := ordered.QueryGroups[index]
			if digest, found := manifestObjectDigest(manifest, group.QueryGroup); !found || digest != group.ObjectDigest {
				t.Fatalf("%s = %q, %t; want %q", group.QueryGroup, digest, found, group.ObjectDigest)
			}
		}
		for _, absent := range []execution.QueryGroupIdentity{"qg-", "qg-0150a", "qg-9999", "a", ""} {
			if digest, found := manifestObjectDigest(manifest, absent); found {
				t.Fatalf("%q, which the manifest does not name, read as %q", absent, digest)
			}
		}
	}
	if _, found := manifestObjectDigest(CatalogManifest{}, "qg-0001"); found {
		t.Fatal("an empty manifest named an object")
	}
}

// Halving, not walking: a lookup in a manifest of a few thousand Query Groups
// costs a dozen comparisons, and it runs on every frozen Slot.
func BenchmarkManifestObjectDigest(b *testing.B) {
	manifest := CatalogManifest{}
	const groups = 2824
	for index := 0; index < groups; index++ {
		manifest.QueryGroups = append(manifest.QueryGroups, ManifestQueryGroup{
			QueryGroup:   execution.QueryGroupIdentity(fmt.Sprintf("%064x", index)),
			ObjectDigest: execution.ObjectDigest(fmt.Sprintf("%064x", index+1)),
		})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		group := manifest.QueryGroups[index%groups]
		if _, found := manifestObjectDigest(manifest, group.QueryGroup); !found {
			b.Fatal("not found")
		}
	}
}
