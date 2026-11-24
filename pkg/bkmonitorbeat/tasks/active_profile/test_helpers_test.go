// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package active_profile

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

// newTestGather 使用真实的全局配置构造 Gather，让终态指标链路也一并被覆盖。
func newTestGather(conf *configs.ActiveProfileTaskConfig) define.Task {
	globalConfig := configs.NewConfig()
	globalConfig.GatherUpBeat.DataID = 1100001
	return New(globalConfig, conf)
}

// newTestTaskConfig 构造一个已通过校验的计划任务单机配置。
func newTestTaskConfig(t *testing.T) *configs.ActiveProfileTaskConfig {
	t.Helper()
	conf := configs.NewActiveProfileTaskConfig()
	conf.BKTenantID = "system"
	conf.BizID = 11
	conf.TaskID = 1001
	conf.BKHostID = 456
	conf.Name = "native-cpu-demo"
	conf.Creator = "admin"
	conf.OutputDir = t.TempDir()
	conf.Schedule = configs.ActiveProfileScheduleConfig{
		Type:           configs.ActiveProfileScheduleCron,
		CronExpression: "0 */2 * * *",
		Timezone:       "Asia/Shanghai",
	}
	conf.ProcessSelector = configs.ActiveProfileProcessSelector{
		Type: configs.ActiveProfileSelectorPID,
		PIDs: "2341;2342",
	}
	conf.ResourceGuardConfig = configs.ActiveProfileResourceGuardConfig{
		AutoStopOnResourcePressure: true,
		MaxDiskUsage:               1024,
		MinMemLevel:                100,
	}
	conf.EventDataID = 110001
	conf.ProfileDataID = 110002
	conf.ProfileAppName = "builtin_profile_app"
	conf.ProfileServiceName = "builtin_profile_app"
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}
	return conf
}

// newTestGoTaskConfig 构造一个已通过校验的 Golang 采集配置。Go 不使用
// process_selector，目标由 pprof endpoint 的 IP 与端口决定。
func newTestGoTaskConfig(t *testing.T, collectionType string) *configs.ActiveProfileTaskConfig {
	t.Helper()
	conf := newTestTaskConfig(t)
	conf.CollectionMethod = configs.ActiveProfileMethodGolang
	conf.CollectionType = collectionType
	conf.Name = "go-" + collectionType + "-demo"
	conf.Creator = "admin"
	conf.ProcessSelector = configs.ActiveProfileProcessSelector{}
	conf.CollectorConfig.IPAddress = "127.0.0.1"
	conf.CollectorConfig.Port = 6060
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}
	return conf
}
