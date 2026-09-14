// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/platformsettings"
)

// A deployment that renders no distribution gets the copy it had before the
// distribution existed: not_configured, answering from its own layer over
// the code defaults, with the compiler facts and the host filter built from
// that answer. Nothing about it is a fault, and the fleet facts say so.
func TestPlatformSettingsWithoutADistributionAreTheDeploymentLayer(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	seven := []string{"备用机", "测试中", "故障中", "运营中[不监控]", "开发中[不监控]", "运营中[无告警]", "开发中[无告警]"}
	enabled := true
	cfg.PhaseTwo.PlatformSettings.HostDisableMonitorStates = &seven
	cfg.PhaseTwo.PlatformSettings.IsAccessBKData = &enabled
	now := time.Unix(1_700_000_000, 0)
	cache, err := buildPlatformSettings(context.Background(), cfg, nil, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if stats := cache.Stats(); stats.Mode != platformsettings.ModeNotConfigured {
		t.Fatalf("mode without a distribution = %s, want not_configured", stats.Mode)
	}
	current := cache.Current()
	if !reflect.DeepEqual(current.HostDisableMonitorStates, seven) || !current.IsAccessBKData ||
		!reflect.DeepEqual(current.FileSystemTypeIgnore, platformsettings.CodeDefaults().FileSystemTypeIgnore) {
		t.Fatalf("current = %+v, want the deployment layer over the code defaults", current)
	}
	facts := legacyQueryRuntimeFacts(cfg, current)
	if facts.AccessBKData == nil || !*facts.AccessBKData || !reflect.DeepEqual(facts.SystemDiskFilter.Values, []string{"iso9660", "tmpfs", "udf"}) {
		t.Fatalf("compiler facts = %+v", facts)
	}
	hostStatus := newDynamicHostStatusFilter(current.HostDisableMonitorStates)
	if got := len(hostStatus.States()); got != 7 {
		t.Fatalf("states in force = %d, want the deployment's 7", got)
	}
	fleetFacts := platformSettingsFactsSource(cache, func() time.Time { return now })()
	if fleetFacts.Mode != "not_configured" || fleetFacts.StaleBeyondBound || fleetFacts.AuthoritativeAgeSeconds != nil {
		t.Fatalf("fleet facts = %+v, want not_configured with no age and no staleness", fleetFacts)
	}
	// The refresher on a copy with no source changes nothing and reports the
	// filter in force.
	recorder := metric.NewRecorder(metric.BuildInfo{})
	platformSettingsRefresher(cache, hostStatus, recorder)(context.Background())
	if got := len(hostStatus.States()); got != 7 {
		t.Fatalf("states in force after a refresh without a source = %d, want 7", got)
	}
}
