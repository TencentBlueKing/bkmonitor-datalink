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
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The dimensions md5 Python's no-data checker builds for the host group
// {bk_target_ip: 192.0.2.10, bk_target_cloud_id: 0}, with and without the tag.
//
// Both were produced by running count_md5 from bkmonitor/utils/common_utils.py
// (the function itself, lifted out of that file unchanged) on the dict the
// checker hashes: _process_dimensions in
// alarm_backends/core/control/mixins/nodata.py reduces a point's dimensions to
// the no-data dimensions, then sets __NO_DATA_DIMENSION__ to True and hashes
// the result. The second literal is the same host's dict without the tag, which
// is what the item's own series hashes to.
const (
	pythonHostGroupNoDataMD5 = "055f2674de15b68920b132aeac4a7fa0"
	pythonHostSeriesMD5      = "7d6679ee7a605ab1011d644138cb2b2a"
	pythonWholeItemMD5       = "3e06a0b6d0560271cafee9f08a6da2d7"
)

// A real series may itself carry a label named like the no-data tag. It still
// projects onto the group Python builds, and that group carries the tag once,
// as the boolean True, whatever text the label held.
//
// Python drops every dimension that is not a no-data dimension before it adds
// the tag (nodata.py, _process_dimensions: the reduction loop pops the label,
// then dimensions.update({NO_DATA_TAG_DIMENSION: True})), so the label never
// reaches the identity: the group's md5 is the one a series without the label
// has. Two things would go wrong if the label did reach it. The memory key
// would carry the tag twice or with the label's text, so the group would be a
// different group from the one the same host reports without the label, and an
// open absence on one would never be closed by data arriving on the other. And
// the dimensions md5 would follow the label's text: "true" and "false" hash to
// something no Python record holds, and the anomaly would read as a new alert
// every round.
func TestARealSeriesCarryingATagNamedLabelProjectsToTheGroupPythonBuilds(t *testing.T) {
	host := map[string]string{"bk_target_ip": "192.0.2.10", "bk_target_cloud_id": "0"}
	clean, ok := Project(host, hostNoDataDimensions)
	if !ok {
		t.Fatal("fixture: the host series does not project")
	}
	for _, label := range []string{"true", "True", "false", "1", ""} {
		t.Run("label "+label, func(t *testing.T) {
			series := map[string]string{contract.NoDataDimensionTag: label}
			for name, value := range host {
				series[name] = value
			}
			group, ok := Project(series, hostNoDataDimensions)
			if !ok {
				t.Fatal("a host series carrying a tag-named label was dropped")
			}
			if group.Key() != clean.Key() {
				t.Fatalf("the series projects to %q, and the same host without the label to %q: one host "+
					"is two groups, and data on one never closes an absence on the other", group.Key(), clean.Key())
			}
			if count := strings.Count(group.Key(), contract.NoDataDimensionTag); count != 1 {
				t.Fatalf("the memory key %q carries the tag %d times, want once", group.Key(), count)
			}
			for _, dimension := range group.Dimensions() {
				if dimension.Name == contract.NoDataDimensionTag {
					t.Fatalf("the group holds the label as its own dimension %+v; the tag is added by the "+
						"identity and is not the series' to set", dimension)
				}
			}
			fields := SyntheticSeries{Group: group}.IdentityFields()
			if got := string(fields[contract.NoDataDimensionTag]); got != "true" {
				t.Fatalf("the identity carries the tag as %s, want the boolean true whatever the label said", got)
			}
			digest, err := contract.PythonObjectMD5(fields)
			if err != nil {
				t.Fatal(err)
			}
			if digest != pythonHostGroupNoDataMD5 {
				t.Fatalf("the group hashes to %s, want Python's %s for the same host", digest, pythonHostGroupNoDataMD5)
			}

			// An item without dimensions: every series is the whole item,
			// the label included.
			item, ok := Project(series, nil)
			if !ok || item.Key() != WholeItemGroup().Key() {
				t.Fatalf("with no no-data dimensions the series projects to %q, want the whole item", item.Key())
			}
			whole, err := contract.PythonObjectMD5(SyntheticSeries{Group: item}.IdentityFields())
			if err != nil {
				t.Fatal(err)
			}
			if whole != pythonWholeItemMD5 {
				t.Fatalf("the whole item hashes to %s, want Python's %s", whole, pythonWholeItemMD5)
			}
		})
	}
}

// The tag is what separates a no-data group from the item's own series: with
// it the host's group hashes to Python's no-data md5, and without it to the
// series' own md5, which is what the threshold anomaly on the same host is
// filed under (nodata-capability-decomposition, section 5.5 R1 and R9).
func TestTheGroupHashesWithTheTagAndTheSeriesWithout(t *testing.T) {
	group, ok := Project(map[string]string{"bk_target_ip": "192.0.2.10", "bk_target_cloud_id": "0"}, hostNoDataDimensions)
	if !ok {
		t.Fatal("fixture: the host series does not project")
	}
	fields := SyntheticSeries{Group: group}.IdentityFields()
	with, err := contract.PythonObjectMD5(fields)
	if err != nil {
		t.Fatal(err)
	}
	delete(fields, contract.NoDataDimensionTag)
	without, err := contract.PythonObjectMD5(fields)
	if err != nil {
		t.Fatal(err)
	}
	if with != pythonHostGroupNoDataMD5 || without != pythonHostSeriesMD5 {
		t.Fatalf("with the tag %s, without %s; want Python's %s and %s", with, without,
			pythonHostGroupNoDataMD5, pythonHostSeriesMD5)
	}
	// And the memory key says the same: the group's key is the tagged one,
	// so it is never the key text of a series without the tag.
	if !strings.HasSuffix(group.Key(), ","+contract.NoDataDimensionTag+"=true") {
		t.Fatalf("the memory key %q does not end with the tag", group.Key())
	}
	if WholeItemGroup().Key() != contract.NoDataDimensionTag+"=true" {
		t.Fatalf("the whole item's memory key is %q, want the tag alone", WholeItemGroup().Key())
	}
}
