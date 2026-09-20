// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/platformsettings"
)

// platformSettingsConfigContents is the go_access fixture with the whole
// phase_two block supplied by the test, so a test can state either layout
// of the platform settings; platformCache is spliced under redis as the
// other fixtures do.
func platformSettingsConfigContents(platformCache, phaseTwo string) string {
	base := platformCacheConfigContents(platformCache)
	return base[:strings.Index(base, "phase_two:\n")] + "phase_two:\n  worker:\n    id: alarmd-worker-0\n" +
		"  access:\n    uq_endpoint: http://unify-query.service\n    query_source: alarmd\n" + phaseTwo
}

const platformSettingsControlBase = "  control:\n    strategy_cache_prefix: alarm-config\n    timezone: Asia/Shanghai\n"

// The deprecated keys, in the shape every deployment states them today, are
// carried into the platform_settings group as its deployment layer, and hold
// nothing afterwards. A network filter is only ever the constant; a disk
// filter field is only ever device_type; a value stated in both places must
// agree. Absent everywhere, the layer is empty and the platform's code
// defaults stand.
func TestDeprecatedPlatformSettingKeysAreCarriedIntoTheGroup(t *testing.T) {
	loaded, err := Load(writeConfig(t, platformSettingsConfigContents("", `    host_disable_monitor_states: ["备用机", "测试中", "故障中", "运营中[不监控]"]
`+platformSettingsControlBase+`    legacy_query_runtime:
      access_bk_data: true
      bkdata_cmdb_level_tables: [system.cpu_summary]
      system_disk_filter:
        field_name: device_type
        values: [iso9660, tmpfs, udf]
      system_network_filter:
        field_name: device_name
        values: [lo]
`)))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	layer := loaded.PhaseTwo.PlatformSettings.Layer()
	if layer.HostDisableMonitorStates == nil || !reflect.DeepEqual(*layer.HostDisableMonitorStates, []string{"备用机", "测试中", "故障中", "运营中[不监控]"}) ||
		layer.IsAccessBKData == nil || !*layer.IsAccessBKData ||
		layer.BKDataCMDBLevelTables == nil || !reflect.DeepEqual(*layer.BKDataCMDBLevelTables, []string{"system.cpu_summary"}) ||
		layer.FileSystemTypeIgnore == nil || !reflect.DeepEqual(*layer.FileSystemTypeIgnore, []string{"iso9660", "tmpfs", "udf"}) {
		t.Fatalf("carried layer = %+v", layer)
	}
	legacy := loaded.PhaseTwo.Control.LegacyQueryRuntime
	if loaded.PhaseTwo.Access.HostDisableMonitorStates != nil || legacy.AccessBKData != nil || legacy.BKDataCMDBLevelTables != nil ||
		legacy.SystemDiskFilter.Values != nil || legacy.SystemNetworkFilter.Values != nil {
		t.Fatalf("the deprecated keys still hold values after being carried: access=%v legacy=%+v", loaded.PhaseTwo.Access.HostDisableMonitorStates, legacy)
	}
	if got := loaded.PhaseTwo.PlatformSettings.RedisKeyPrefix; got != platformsettings.DefaultKeyPrefix {
		t.Fatalf("prefix = %q, want the platform default when unstated", got)
	}
	resolved := platformsettings.Resolve(platformsettings.CodeDefaults(), layer)
	if !resolved.IsAccessBKData || len(resolved.HostDisableMonitorStates) != 4 {
		t.Fatalf("resolved through the deployment layer = %+v", resolved)
	}
}

func TestPlatformSettingsGroupIsReadDirectlyAndRefusesContradictions(t *testing.T) {
	loaded, err := Load(writeConfig(t, platformSettingsConfigContents("", platformSettingsControlBase+`  platform_settings:
    redis_key_prefix: "bk_monitor_base_test:"
    host_disable_monitor_states: ["备用机"]
    is_access_bk_data: false
    bkdata_cmdb_level_tables: []
`)))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	layer := loaded.PhaseTwo.PlatformSettings.Layer()
	if loaded.PhaseTwo.PlatformSettings.RedisKeyPrefix != "bk_monitor_base_test:" || layer.HostDisableMonitorStates == nil ||
		layer.IsAccessBKData == nil || *layer.IsAccessBKData || layer.BKDataCMDBLevelTables == nil || len(*layer.BKDataCMDBLevelTables) != 0 ||
		layer.FileSystemTypeIgnore != nil {
		t.Fatalf("group layer = %+v (prefix %q)", layer, loaded.PhaseTwo.PlatformSettings.RedisKeyPrefix)
	}
	for name, extra := range map[string]string{
		"both places disagree": `    host_disable_monitor_states: ["备用机"]
` + platformSettingsControlBase + `  platform_settings:
    host_disable_monitor_states: ["测试中"]
`,
		"access_bk_data stated twice differently": platformSettingsControlBase + `    legacy_query_runtime:
      access_bk_data: true
  platform_settings:
    is_access_bk_data: false
`,
		"disk filter field is not the constant": platformSettingsControlBase + `    legacy_query_runtime:
      system_disk_filter:
        field_name: fstype
        values: [tmpfs]
`,
		"network filter is not a setting": platformSettingsControlBase + `    legacy_query_runtime:
      system_network_filter:
        field_name: device_name
        values: []
`,
		"network filter field is not the constant": platformSettingsControlBase + `    legacy_query_runtime:
      system_network_filter:
        field_name: eth
        values: [lo]
`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, platformSettingsConfigContents("", extra))); err == nil {
				t.Fatal("Load() accepted a configuration that says two things about one setting")
			}
		})
	}
	// Both places agreeing is not a contradiction.
	if _, err := Load(writeConfig(t, platformSettingsConfigContents("", `    host_disable_monitor_states: ["备用机"]
`+platformSettingsControlBase+`  platform_settings:
    host_disable_monitor_states: ["备用机"]
`))); err != nil {
		t.Fatalf("Load() with both places agreeing: %v", err)
	}
}

// The distribution's connection is rendered or absent, never inherited: an
// inherited connection would read alarmd's own store as "nothing published"
// and the copy would call the deployment not_configured for the wrong reason.
func TestDynamicConfigConnectionIsStatedOrAbsent(t *testing.T) {
	loaded, err := Load(writeConfig(t, platformSettingsConfigContents("", platformSettingsControlBase)))
	if err != nil {
		t.Fatal(err)
	}
	if _, configured := loaded.DynamicConfigRedis(); configured {
		t.Fatal("an unstated distribution connection resolved to something")
	}
	loaded, err = Load(writeConfig(t, platformSettingsConfigContents(`
platform_cache:
  dynamic_config:
    mode: standalone
    address: platform-default:6379
    db: 0`, platformSettingsControlBase)))
	if err != nil {
		t.Fatal(err)
	}
	connection, configured := loaded.DynamicConfigRedis()
	if !configured || connection.Address != "platform-default:6379" || connection.DialTimeout == 0 || connection.ReadTimeout == 0 {
		t.Fatalf("distribution connection = (%+v, %t), want the stated instance with the process timeouts inherited", connection, configured)
	}
}
