// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata

import (
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// Python drops a point whose dimensions do not carry every no-data dimension:
// set(no_data_dimensions) - set(dimensions.keys()) non-empty means invalid,
// and the point is logged and skipped rather than reduced.
func TestProjectDropsSeriesMissingAnyNoDataDimension(t *testing.T) {
	for name, dimensions := range map[string]map[string]string{
		"missing one":  {"bk_target_ip": "10.0.0.1"},
		"missing both": {"unrelated": "x"},
		"empty":        {},
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := Project(dimensions, []string{"bk_target_ip", "bk_target_cloud_id"}); ok {
				t.Fatal("Project() accepted a series missing a no-data dimension")
			}
		})
	}
}

// A dimension the series carries but the item did not ask for is dropped from
// the group rather than making the series invalid: Python's invalid test is
// one-directional, and the reduction that follows it pops every key that is
// not a no-data dimension.
func TestProjectKeepsOnlyTheRequestedDimensions(t *testing.T) {
	group, ok := Project(map[string]string{
		"bk_target_ip": "10.0.0.1", "bk_target_cloud_id": "0", "device": "eth0",
	}, []string{"bk_target_cloud_id", "bk_target_ip"})
	if !ok {
		t.Fatal("Project() rejected a series carrying every no-data dimension")
	}
	want := []Dimension{{Name: "bk_target_cloud_id", Value: "0"}, {Name: "bk_target_ip", Value: "10.0.0.1"}}
	if got := group.Dimensions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Dimensions() = %+v, want %+v in name order", got, want)
	}
}

// The order the item states its dimensions in is not part of the group: two
// series that agree on the pairs are one group however either side listed them.
func TestGroupIdentityIsIndependentOfStatedOrder(t *testing.T) {
	dimensions := map[string]string{"b": "2", "a": "1"}
	first, _ := Project(dimensions, []string{"a", "b"})
	second, _ := Project(dimensions, []string{"b", "a"})
	if first.Key() != second.Key() {
		t.Fatalf("Key() = %q and %q for the same pairs", first.Key(), second.Key())
	}
}

// Empty agg_dimension is a setting, not a gap: every series becomes the one
// group, and none of them is invalid however few dimensions it carries.
func TestEmptyAggDimensionReducesEverySeriesToTheWholeItemGroup(t *testing.T) {
	for _, dimensions := range []map[string]string{{}, {"device": "eth0"}, nil} {
		group, ok := Project(dimensions, nil)
		if !ok {
			t.Fatalf("Project(%v, nil) reported invalid", dimensions)
		}
		if got, want := group.Key(), WholeItemGroup().Key(); got != want {
			t.Fatalf("Key() = %q, want the whole-item group %q", got, want)
		}
		if len(group.Dimensions()) != 0 {
			t.Fatalf("whole-item group carries %+v", group.Dimensions())
		}
	}
}

// The tag is on every group, including the whole-item one, and is the only
// thing that group carries.
func TestEveryGroupCarriesTheNoDataTag(t *testing.T) {
	group, _ := Project(map[string]string{"a": "1"}, []string{"a"})
	for _, key := range []string{group.Key(), WholeItemGroup().Key()} {
		if !strings.Contains(key, contract.NoDataDimensionTag) {
			t.Fatalf("Key() = %q, want it to carry %s", key, contract.NoDataDimensionTag)
		}
	}
}

// A value holding the encoding's separators must not let one group masquerade
// as another. Without escaping, {a: "b,c=d"} and {a: "b", c: "d"} produce the
// same text, which would merge two objects into one alert.
func TestGroupKeySeparatorsInValuesDoNotCollide(t *testing.T) {
	first, _ := Project(map[string]string{"a": "b,c=d"}, []string{"a"})
	second, _ := Project(map[string]string{"a": "b", "c": "d"}, []string{"a", "c"})
	if first.Key() == second.Key() {
		t.Fatalf("distinct groups share the key %q", first.Key())
	}
}

// Series that reduce to the same group are one group, and the dropped ones are
// counted rather than lost: a strategy naming a dimension its data never
// carries drops every series every round, and the count is what says so.
func TestProjectSeriesFoldsDuplicatesAndCountsDropped(t *testing.T) {
	tally := ProjectSeries([]map[string]string{
		{"host": "a", "device": "eth0"},
		{"host": "a", "device": "eth1"},
		{"host": "b", "device": "eth0"},
		{"device": "eth0"},
	}, []string{"host"})
	if len(tally.Groups) != 2 {
		t.Fatalf("groups = %d, want the two distinct hosts", len(tally.Groups))
	}
	if tally.Dropped != 1 {
		t.Fatalf("dropped = %d, want the one series with no host", tally.Dropped)
	}
}
