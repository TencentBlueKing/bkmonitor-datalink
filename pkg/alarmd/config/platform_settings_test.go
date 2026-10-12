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
	"strings"
	"testing"
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

// The platform_settings group is the deployment layer, read as stated.
func TestPlatformSettingsGroupIsReadDirectly(t *testing.T) {
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
