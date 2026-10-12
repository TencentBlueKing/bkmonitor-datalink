// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"testing"
)

// The object cache's decoded reading is on the scrape once bound: the last
// and largest ratio and how many objects it rests on.
func TestTheScrapeReadsTheObjectCachesDecodedRatio(t *testing.T) {
	r := NewRecorder(BuildInfo{Version: "test"})
	if err := r.BindDecodedObjects(func() (uint64, float64, float64) { return 3, 1.1, 1.4 }); err != nil {
		t.Fatal(err)
	}
	families, err := r.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	readings := map[string]float64{}
	for _, family := range families {
		for _, series := range family.GetMetric() {
			key := family.GetName()
			for _, label := range series.GetLabel() {
				key += "/" + label.GetValue()
			}
			readings[key] = series.GetGauge().GetValue() + series.GetCounter().GetValue()
		}
	}
	for key, want := range map[string]float64{
		"bkmonitor_alarmd_object_cache_decoded_ratio/last":    1.1,
		"bkmonitor_alarmd_object_cache_decoded_ratio/max":     1.4,
		"bkmonitor_alarmd_object_cache_decoded_samples_total": 3,
	} {
		if readings[key] != want {
			t.Errorf("%s = %v, want %v", key, readings[key], want)
		}
	}
}
