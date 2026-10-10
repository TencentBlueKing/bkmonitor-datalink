// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package platformsettings

import (
	"reflect"
	"testing"
)

// The keys are the protocol's formulas verbatim: prefix as given, the
// tenant percent-encoded the way Python's quote(safe="") does it, the DB
// key under the field's owning domain.
func TestKeysFollowTheProtocolFormulas(t *testing.T) {
	if got := RevisionKey(DefaultKeyPrefix); got != "bk_monitor_base:dynamic_config:revision" {
		t.Fatalf("revision key = %s", got)
	}
	if got := ConfigKey(DefaultKeyPrefix, Tenant, FieldHostDisableMonitorStates.DBKey()); got !=
		"bk_monitor_base:dynamic_config:{system}:base_config.metadata.host_disable_monitor_states" {
		t.Fatalf("config key = %s", got)
	}
	for tenant, want := range map[string]string{"tenant/a": "tenant%2Fa", "a b": "a%20b", "ok-._~": "ok-._~", "租户": "%E7%A7%9F%E6%88%B7"} {
		if got := encodeTenant(tenant); got != want {
			t.Fatalf("encodeTenant(%q) = %s, want %s", tenant, got, want)
		}
	}
	for _, prefix := range []string{"", "bk{x}:", "bk :"} {
		if err := ValidateKeyPrefix(prefix); err == nil {
			t.Fatalf("prefix %q was accepted", prefix)
		}
	}
	if err := ValidateKeyPrefix("bk_monitor_base:"); err != nil {
		t.Fatal(err)
	}
	for _, field := range Fields {
		if !ValidField(field) {
			t.Fatalf("%s is listed but not valid", field)
		}
	}
	if ValidField("made_up") {
		t.Fatal("a field outside the closed set is valid")
	}
}

// The fallback rule is the protocol's, applied to alarmd's own layers: a
// layer's value stands when it is present and differs from the code
// default; equal to the default it falls through to the next layer; absent
// everywhere the default stands. The table is the protocol's own example
// (default 30, env 60) transposed onto the deployment layer.
func TestResolveAppliesTheProtocolFallbackRule(t *testing.T) {
	defaults := CodeDefaults()
	list := func(values ...string) *[]string { return &values }
	boolean := func(value bool) *bool { return &value }
	seven := []string{"备用机", "测试中", "故障中", "运营中[不监控]", "开发中[不监控]", "运营中[无告警]", "开发中[无告警]"}
	for _, arm := range []struct {
		name       string
		platform   Layer
		deployment Layer
		want       Settings
	}{
		{name: "nothing declared anywhere: the code defaults", want: defaults},
		{name: "the deployment layer alone", deployment: Layer{HostDisableMonitorStates: list(seven...)},
			want: Settings{HostDisableMonitorStates: seven, BKDataCMDBLevelTables: []string{}, FileSystemTypeIgnore: defaults.FileSystemTypeIgnore}},
		{name: "the platform overrides the deployment", platform: Layer{HostDisableMonitorStates: list("备用机")},
			deployment: Layer{HostDisableMonitorStates: list(seven...)},
			want:       Settings{HostDisableMonitorStates: []string{"备用机"}, BKDataCMDBLevelTables: []string{}, FileSystemTypeIgnore: defaults.FileSystemTypeIgnore}},
		{name: "a platform value equal to the default falls through to the deployment",
			platform:   Layer{HostDisableMonitorStates: list("备用机", "测试中", "故障中"), IsAccessBKData: boolean(false)},
			deployment: Layer{HostDisableMonitorStates: list(seven...), IsAccessBKData: boolean(true)},
			want:       Settings{HostDisableMonitorStates: seven, IsAccessBKData: true, BKDataCMDBLevelTables: []string{}, FileSystemTypeIgnore: defaults.FileSystemTypeIgnore}},
		{name: "a deployment value equal to the default is the default",
			deployment: Layer{FileSystemTypeIgnore: list("iso9660", "tmpfs", "udf")}, want: defaults},
		{name: "an empty list is a value when the default is not empty",
			platform: Layer{FileSystemTypeIgnore: list()},
			want:     Settings{HostDisableMonitorStates: defaults.HostDisableMonitorStates, BKDataCMDBLevelTables: []string{}, FileSystemTypeIgnore: []string{}}},
		{name: "false and the empty list are the defaults of their fields",
			platform: Layer{IsAccessBKData: boolean(false), BKDataCMDBLevelTables: list()}, deployment: Layer{BKDataCMDBLevelTables: list("system.cpu_summary")},
			want: Settings{HostDisableMonitorStates: defaults.HostDisableMonitorStates, BKDataCMDBLevelTables: []string{"system.cpu_summary"}, FileSystemTypeIgnore: defaults.FileSystemTypeIgnore}},
	} {
		t.Run(arm.name, func(t *testing.T) {
			got := Resolve(defaults, arm.platform, arm.deployment)
			// No arm of this table states a horizon, so each resolves the
			// contract's default; the horizon's own rule is tested apart.
			want := arm.want
			want.NoDataTrackingHorizonSeconds, want.NoDataTrackingHorizonSource = DefaultNoDataTrackingHorizonSeconds, HorizonSourceDefault
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Resolve() = %+v, want %+v", got, want)
			}
		})
	}
	// Resolve never aliases a layer's slice: the effective settings are
	// handed out under a read lock and must not change under the reader.
	shared := []string{"a"}
	resolved := Resolve(defaults, Layer{HostDisableMonitorStates: &shared})
	shared[0] = "b"
	if resolved.HostDisableMonitorStates[0] != "a" {
		t.Fatal("the resolved settings alias the layer's slice")
	}
}

func TestChangedFieldsNamesEveryDifference(t *testing.T) {
	defaults := CodeDefaults()
	other := defaults
	other.IsAccessBKData = true
	other.FileSystemTypeIgnore = []string{"tmpfs"}
	if got := defaults.ChangedFields(other); !reflect.DeepEqual(got, []Field{FieldIsAccessBKData, FieldFileSystemTypeIgnore}) {
		t.Fatalf("ChangedFields = %v", got)
	}
	if !defaults.Equal(CodeDefaults()) || defaults.Equal(other) {
		t.Fatal("Equal disagrees with ChangedFields")
	}
	// nil and the empty list are the same value: the protocol's JSON has no
	// way to say nil.
	if !(Settings{BKDataCMDBLevelTables: nil}).Equal(Settings{BKDataCMDBLevelTables: []string{}}) {
		t.Fatal("nil and empty read as different lists")
	}
}
