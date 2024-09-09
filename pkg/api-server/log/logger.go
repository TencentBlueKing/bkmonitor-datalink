// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package log

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/api-server/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// InitLogger logger initial
func InitLogger() {
	maxSize := config.Config.Log.MaxSize
	if maxSize == 0 {
		maxSize = 100
	}
	// 默认 1day
	maxAge := config.Config.Log.MaxAge
	if maxAge == 0 {
		maxAge = 1
	}
	// 默认 5
	maxBackups := config.Config.Log.MaxBackups
	if maxBackups == 0 {
		maxBackups = 5
	}

	logger.SetOptions(logger.Options{
		Stdout:     config.Config.Log.EnableStdout,
		Level:      config.Config.Log.Level,
		Filename:   config.Config.Log.Path,
		MaxSize:    maxSize,
		MaxAge:     maxAge,
		MaxBackups: maxBackups,
	})
}
