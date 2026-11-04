// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package executor

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
)

// newTestTaskConfig 构造一个已通过校验的单机执行配置，供 RunCPU 级别的测试使用。
func newTestTaskConfig(t *testing.T, pids ...int32) *configs.ActiveProfileTaskConfig {
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
		Type:      configs.ActiveProfileScheduleOnceAt,
		ExecuteAt: time.Now().Add(time.Hour).UnixMilli(),
		Timezone:  "UTC",
	}
	conf.ProcessSelector = configs.ActiveProfileProcessSelector{
		Type: configs.ActiveProfileSelectorPID,
		PIDs: selectorPIDs(pids),
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

func selectorPIDs(pids []int32) string {
	items := make([]string, len(pids))
	for i, pid := range pids {
		items[i] = strconv.Itoa(int(pid))
	}
	return strings.Join(items, configs.ActiveProfileSelectorSeparator)
}
