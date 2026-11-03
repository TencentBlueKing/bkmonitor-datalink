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
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/platformsettings"
)

// The host status filter decides on what the platform settings copy
// answers, and follows it. With nothing declared anywhere that is the
// platform's code default -- the same list the platform's own consumers
// apply with nothing declared -- and not "no filter": a deployment whose
// list differs states it in its own layer, as every deployment did before
// the distribution existed. An empty list, from any layer, disables no host
// by state and still drops the invalid and unknown hosts: the filter stays in
// the chain, as Python installs it whatever the list. A change of the list
// swaps the filter under the running chain, and the count published beside it
// follows the filter in force, never the configuration.
func TestHostStatusFilterFollowsThePlatformSettingsCopy(t *testing.T) {
	defaults := platformsettings.CodeDefaults()
	hostStatus := newDynamicHostStatusFilter(defaults.HostDisableMonitorStates)
	if names := filterNames(seriesAdmissionFilters(hostStatus, nil)); !reflect.DeepEqual(names, []string{"target_scope", "target_plan", "host_status"}) {
		t.Fatalf("filters = %v, want the two target forms then the host state", names)
	}
	if got := hostDisableMonitorStateCount(seriesAdmissionFilters(hostStatus, nil)); got != len(defaults.HostDisableMonitorStates) {
		t.Fatalf("states in force = %d, want the code default's %d", got, len(defaults.HostDisableMonitorStates))
	}
	facts := &admission.Facts{HostNaming: admission.HostNaming{NamedID: true, Usable: true, IDKey: "42"}, HostResolved: true, HostState: "备用机"}
	if decision := hostStatus.Admit(admission.PlanContext{}, facts); decision.Admit || decision.Reason != "monitoring_disabled" {
		t.Fatalf("a host in a disabled state was admitted: %+v", decision)
	}
	// The platform publishes a different list while one is in force: the
	// filter swaps, it does not keep the first.
	if got := hostStatus.Apply([]string{"运营中[无告警]"}); got != 1 || !reflect.DeepEqual(hostStatus.States(), []string{"运营中[无告警]"}) {
		t.Fatalf("states in force after a swap = %d %v, want the new list", got, hostStatus.States())
	}
	if decision := hostStatus.Admit(admission.PlanContext{}, facts); !decision.Admit {
		t.Fatalf("a host in a state the new list does not name was refused: %+v", decision)
	}
	// The platform publishes an empty list: no state disables a host, so the
	// same host is admitted; the count says zero.
	if got := hostStatus.Apply([]string{}); got != 0 {
		t.Fatalf("states in force after an empty list = %d, want 0", got)
	}
	if decision := hostStatus.Admit(admission.PlanContext{}, facts); !decision.Admit {
		t.Fatalf("with no states in force the host was refused: %+v", decision)
	}
	// The platform publishes a longer list: the filter follows it.
	if got := hostStatus.Apply([]string{"备用机", "运营中[无告警]"}); got != 2 {
		t.Fatalf("states in force after a new list = %d, want 2", got)
	}
	facts.HostState = "运营中[无告警]"
	if decision := hostStatus.Admit(admission.PlanContext{}, facts); decision.Admit {
		t.Fatalf("a host in a newly disabled state was admitted: %+v", decision)
	}
	// Given no filter, the chain still has one, over no states: a host CMDB
	// does not know is dropped, one it knows is admitted.
	chain := seriesAdmissionFilters(nil, nil)
	if names := filterNames(chain); !reflect.DeepEqual(names, []string{"target_scope", "target_plan", "host_status"}) {
		t.Fatalf("filters given no host filter = %v, want the host state still in the chain", names)
	}
	unknown := &admission.Facts{HostNaming: admission.HostNaming{NamedID: true, Usable: true, IDKey: "43"}}
	if decision := chain[2].Admit(admission.PlanContext{}, unknown); decision.Admit || decision.Reason != "host_unknown" {
		t.Fatalf("an unknown host with no filter given = %+v, want host_unknown", decision)
	}
	if decision := chain[2].Admit(admission.PlanContext{}, facts); !decision.Admit {
		t.Fatalf("a known host with no filter given was refused: %+v", decision)
	}
}

func filterNames[T interface{ Name() string }](filters []T) []string {
	names := make([]string, 0, len(filters))
	for _, filter := range filters {
		names = append(names, filter.Name())
	}
	return names
}
