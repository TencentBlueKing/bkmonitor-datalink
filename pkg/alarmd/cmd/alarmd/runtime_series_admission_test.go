// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
)

// The host status filter must not appear by default. Python's shipped default
// list is not this environment's list, so a filter installed without the
// deployment saying so would drop records on a guess.
func TestHostStatusFilterIsInstalledOnlyWhenStated(t *testing.T) {
	cfg := config.Default()
	if names := filterNames(seriesAdmissionFilters(cfg)); len(names) != 1 || names[0] != "target_scope" {
		t.Fatalf("default filters = %v, want only the monitoring target", names)
	}

	cfg.PhaseTwo.Access.HostDisableMonitorStates = []string{"备用机", "运营中[无告警]"}
	names := filterNames(seriesAdmissionFilters(cfg))
	if len(names) != 2 || names[0] != "target_scope" || names[1] != "host_status" {
		t.Fatalf("configured filters = %v, want the target then the host state", names)
	}
}

// An explicitly empty list is a statement that no host is disabled, and it
// installs nothing rather than a filter that always says yes.
func TestAnEmptyStateListInstallsNoHostStatusFilter(t *testing.T) {
	cfg := config.Default()
	cfg.PhaseTwo.Access.HostDisableMonitorStates = []string{}
	if names := filterNames(seriesAdmissionFilters(cfg)); len(names) != 1 {
		t.Fatalf("filters = %v, want only the monitoring target", names)
	}
}

func filterNames[T interface{ Name() string }](filters []T) []string {
	names := make([]string, 0, len(filters))
	for _, filter := range filters {
		names = append(names, filter.Name())
	}
	return names
}
