// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestPhysicalTableRouteRoundTrip(t *testing.T) {
	var original ResultTableDetail
	err := json.Unmarshal([]byte(`{"physical_tables":{"main":{"database":"logs","table":"new","analyzed_fields":["log"]}},"storage_cluster_records":[{"storage_id":1,"physical_tables":{"main":{"database":"logs","table":"old"}}}]}`), &original)
	if err != nil {
		t.Fatal(err)
	}
	data, err := original.MarshalMsg(nil)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ResultTableDetail
	_, err = decoded.UnmarshalMsg(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original.PhysicalTables, decoded.PhysicalTables) || !reflect.DeepEqual(original.StorageClusterRecords, decoded.StorageClusterRecords) {
		t.Fatalf("physical routes lost: %+v", decoded)
	}
}

func TestPhysicalTableRouteFormats(t *testing.T) {
	const qualified = `"logs.events"`
	const object = `{"database":"logs","table":"events","analyzed_fields":["log"]}`
	for _, current := range []string{qualified, object} {
		for _, history := range []string{qualified, object} {
			raw := `{"physical_tables":{"main":` + current + `},"storage_cluster_records":[{"physical_tables":{"main":` + history + `}}]}`
			var route, cached ResultTableDetail
			if err := json.Unmarshal([]byte(raw), &route); err != nil {
				t.Fatal(err)
			}
			data, err := route.MarshalMsg(nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = cached.UnmarshalMsg(data); err != nil {
				t.Fatal(err)
			}
			for _, detail := range []*ResultTableDetail{&route, &cached} {
				for i, table := range []PhysicalTable{detail.PhysicalTables["main"], detail.StorageClusterRecords[0].PhysicalTables["main"]} {
					want := PhysicalTable{Database: "logs", Table: "events"}
					if []string{current, history}[i] == object {
						want.AnalyzedFields = []string{"log"}
					}
					if !reflect.DeepEqual(table, want) {
						t.Fatalf("route %s, member %d: got %+v, want %+v", raw, i, table, want)
					}
				}
			}
		}
	}
}
