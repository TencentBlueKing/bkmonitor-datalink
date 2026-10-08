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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/platformsettings"
)

// The scrape reads what alarmd acts on for each platform setting and the
// layer it came from: a deployment without the CLI reads it here in one
// scrape - is_access_bk_data decides whether a CMDB-level query reads the
// base table, and nothing said which value was in force.
func TestTheScrapeReadsEachSettingsValueAndLayer(t *testing.T) {
	r := NewRecorder(BuildInfo{Version: "test"})
	settings := platformsettings.CodeDefaults()
	settings.IsAccessBKData = true
	settings.BKDataCMDBLevelTables = []string{"a", "b"}
	sources := map[platformsettings.Field]platformsettings.HorizonSource{}
	for _, field := range platformsettings.Fields {
		sources[field] = platformsettings.HorizonSourceDefault
	}
	sources[platformsettings.FieldIsAccessBKData] = platformsettings.HorizonSourceValues
	r.SetPlatformSettingsSource(func() platformsettings.Stats {
		return platformsettings.Stats{Mode: platformsettings.ModeNotConfigured, Settings: settings, Sources: sources}
	})
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
			readings[key] = series.GetGauge().GetValue()
		}
	}
	for key, want := range map[string]float64{
		"bkmonitor_alarmd_platform_setting_enabled/is_access_bk_data":              1,
		"bkmonitor_alarmd_platform_setting_source/is_access_bk_data/VALUES":        1,
		"bkmonitor_alarmd_platform_setting_source/is_access_bk_data/DYNAMIC":       0,
		"bkmonitor_alarmd_platform_setting_source/file_system_type_ignore/DEFAULT": 1,
		"bkmonitor_alarmd_platform_setting_entries/bkdata_cmdb_level_tables":       2,
		"bkmonitor_alarmd_platform_setting_entries/host_disable_monitor_states":    3,
	} {
		if got, found := readings[key]; !found || got != want {
			t.Errorf("%s = %v (present %v), want %v", key, got, found, want)
		}
	}
}

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
