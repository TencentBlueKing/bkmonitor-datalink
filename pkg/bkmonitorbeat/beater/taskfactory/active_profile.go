// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

//go:build activeprofile || basetask

package taskfactory

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/active_profile"
)

func init() {
	SetTaskConfigByName(define.ModuleActiveProfile, func() define.TaskMetaConfig {
		return new(configs.ActiveProfileMetaConfig)
	})
	Register(define.ModuleActiveProfile, active_profile.New)
}
