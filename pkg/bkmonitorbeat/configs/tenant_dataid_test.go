// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package configs

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tenant"
)

func TestExpectedTenantDataIDsDisableStaticFallback(t *testing.T) {
	storage := tenant.DefaultStorage()
	storage.SetExpectedTasks([]string{
		define.ModuleBasereport,
		define.ModuleExceptionbeat,
		define.ModuleProcessbeat + "_perf",
		define.ModuleProcessbeat + "_port",
		define.ModuleGatherUpBeat,
	})
	t.Cleanup(func() { storage.SetExpectedTasks(nil) })

	baseReport := &BasereportConfig{BaseTaskParam: NewBaseTaskParam()}
	baseReport.DataID = 1001
	if got := len(baseReport.GetTaskConfigList()); got != 0 {
		t.Fatalf("basereport task count = %d, want 0 before tenant data ID is available", got)
	}

	exceptionBeat := &ExceptionBeatConfig{BaseTaskParam: NewBaseTaskParam()}
	exceptionBeat.DataID = 1000
	if got := len(exceptionBeat.GetTaskConfigList()); got != 0 {
		t.Fatalf("exceptionbeat task count = %d, want 0 before tenant data ID is available", got)
	}

	processBeat := &ProcessbeatConfig{
		PortDataId: 1013,
		PerfDataId: 1007,
		Processes:  []ProcessbeatPortConfig{{Name: "example"}},
	}
	if err := processBeat.InitIdent(); err != nil {
		t.Fatalf("initialize processbeat identity: %v", err)
	}
	if got := len(processBeat.GetTaskConfigList()); got != 0 {
		t.Fatalf("processbeat task count = %d, want 0 before tenant data IDs are available", got)
	}

	globalConfig := NewConfig()
	globalConfig.GatherUpBeat.DataID = 1100017
	if got := globalConfig.GetGatherUpDataID(); got != 0 {
		t.Fatalf("gather-up data ID = %d, want 0 before tenant data ID is available", got)
	}

	storage.UpdateTaskDataIDs(map[string]int32{
		define.ModuleBasereport:            2001,
		define.ModuleExceptionbeat:         2000,
		define.ModuleProcessbeat + "_perf": 2007,
		define.ModuleProcessbeat + "_port": 2013,
		define.ModuleGatherUpBeat:          2100017,
	})

	baseReport = &BasereportConfig{BaseTaskParam: NewBaseTaskParam()}
	baseReport.DataID = 1001
	if got := len(baseReport.GetTaskConfigList()); got != 1 || baseReport.DataID != 2001 {
		t.Fatalf("basereport task = (count %d, data ID %d), want (1, 2001)", got, baseReport.DataID)
	}

	exceptionBeat = &ExceptionBeatConfig{BaseTaskParam: NewBaseTaskParam()}
	exceptionBeat.DataID = 1000
	if got := len(exceptionBeat.GetTaskConfigList()); got != 1 || exceptionBeat.DataID != 2000 {
		t.Fatalf("exceptionbeat task = (count %d, data ID %d), want (1, 2000)", got, exceptionBeat.DataID)
	}

	processBeat = &ProcessbeatConfig{
		PortDataId: 1013,
		PerfDataId: 1007,
		Processes:  []ProcessbeatPortConfig{{Name: "example"}},
	}
	if err := processBeat.InitIdent(); err != nil {
		t.Fatalf("initialize processbeat identity after data IDs are available: %v", err)
	}
	if got := len(processBeat.GetTaskConfigList()); got != 1 || processBeat.PortDataId != 2013 || processBeat.PerfDataId != 2007 {
		t.Fatalf(
			"processbeat task = (count %d, port data ID %d, perf data ID %d), want (1, 2013, 2007)",
			got,
			processBeat.PortDataId,
			processBeat.PerfDataId,
		)
	}

	if got := globalConfig.GetGatherUpDataID(); got != 2100017 {
		t.Fatalf("gather-up data ID = %d, want 2100017", got)
	}
}
