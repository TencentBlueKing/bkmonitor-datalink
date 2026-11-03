// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package core

import (
	"go.uber.org/zap"

	monitorLogger "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var logger = monitorLogger.With(zap.String("location", "profiling.core"))
