// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package static_test

import (
	"context"
	"testing"

	"github.com/elastic/beats/libbeat/common"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/static"
)

func newTestReport() *static.Report {
	return &static.Report{
		CPU:    &static.CPU{Total: 8, Model: "test-model"},
		Memory: &static.Memory{Total: 1024},
		Disk:   &static.Disk{Total: 2048},
		Net: &static.Net{Interface: []static.Interface{
			{Addrs: []string{"127.0.0.1"}, Mac: "aa:bb:cc:dd:ee:ff", Name: "eth0"},
			{Addrs: []string{"10.0.0.1"}, Mac: "11:22:33:44:55:66", Name: "eth1"},
		}},
		System: &static.System{
			HostName:      "test-host",
			OS:            "linux",
			Platform:      "centos",
			PlatVer:       "7.9",
			SysType:       "64-bit",
			Arch:          "x86_64",
			KernelVersion: "3.10.0",
		},
	}
}

func TestGetDataAppliesReportFields(t *testing.T) {
	st := gostub.New()
	defer st.Reset()

	st.Stub(&static.GetCPUStatus, func(ctx context.Context) (*static.CPU, error) {
		return &static.CPU{Total: 1, Model: "test-model"}, nil
	})
	st.Stub(&static.GetMemoryStatus, func(ctx context.Context) (*static.Memory, error) {
		return &static.Memory{Total: 2}, nil
	})
	st.Stub(&static.GetDiskStatus, func(ctx context.Context) (*static.Disk, error) {
		return &static.Disk{Total: 3}, nil
	})
	st.Stub(&static.GetNetStatus, func(ctx context.Context, cfg *configs.StaticTaskConfig) (*static.Net, error) {
		return &static.Net{}, nil
	})
	st.Stub(&static.GetSystemStatus, func(ctx context.Context) (*static.System, error) {
		return &static.System{OS: "linux"}, nil
	})

	report, err := static.GetData(context.Background(), &configs.StaticTaskConfig{ReportFields: []string{"system.os"}})
	assert.NoError(t, err)

	data := report.AsMapStr()
	assert.NotContains(t, data, "cpu")
	assert.Equal(t, "linux", data["system"].(common.MapStr)["os"])
}

func TestReportAsMapStrAllFields(t *testing.T) {
	report := newTestReport()
	assert.NoError(t, report.SetReportFields(nil))

	data := report.AsMapStr()
	assert.Contains(t, data, "cpu")
	assert.Contains(t, data, "disk")
	assert.Contains(t, data, "mem")
	assert.Contains(t, data, "net")
	assert.Contains(t, data, "system")

	system := data["system"].(common.MapStr)
	assert.Equal(t, "test-host", system["hostname"])
	assert.Equal(t, "linux", system["os"])
	assert.Equal(t, "x86", system["arch"])
	assert.Equal(t, "centos", system["platform"])
	assert.Equal(t, "7.9", system["platVer"])
	assert.Equal(t, "64-bit", system["sysType"])
	assert.Equal(t, "3.10.0", system["kernelVersion"])
}

func TestReportAsMapStrFilteredSystemFields(t *testing.T) {
	report := newTestReport()
	assert.NoError(t, report.SetReportFields([]string{"system.os", "system.platVer"}))

	data := report.AsMapStr()
	assert.NotContains(t, data, "cpu")
	assert.NotContains(t, data, "disk")
	assert.NotContains(t, data, "mem")
	assert.NotContains(t, data, "net")

	system := data["system"].(common.MapStr)
	assert.Equal(t, "linux", system["os"])
	assert.Equal(t, "7.9", system["platVer"])
	assert.NotContains(t, system, "platform")
	assert.NotContains(t, system, "kernelVersion")
}

func TestReportAsMapStrFilteredNetInterfaceFields(t *testing.T) {
	report := newTestReport()
	assert.NoError(t, report.SetReportFields([]string{"net.interface.mac"}))

	data := report.AsMapStr()
	netBlock := data["net"].(common.MapStr)
	interfaces := netBlock["interface"].([]common.MapStr)
	assert.Len(t, interfaces, 2)
	assert.Equal(t, "aa:bb:cc:dd:ee:ff", interfaces[0]["mac"])
	assert.NotContains(t, interfaces[0], "name")
	assert.NotContains(t, interfaces[0], "addrs")
}

func TestReportAsMapStrNilSystem(t *testing.T) {
	report := &static.Report{}
	assert.NotPanics(t, func() {
		_ = report.AsMapStr()
	})
	assert.NotContains(t, report.AsMapStr(), "system")
}

func TestReportSetReportFieldsInvalid(t *testing.T) {
	report := newTestReport()
	assert.Error(t, report.SetReportFields([]string{"system.not_exists"}))
}
