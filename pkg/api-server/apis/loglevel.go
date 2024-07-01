// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package apis

import (
	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// SetLogLevel 动态设置日志级别
func SetLogLevel(c *gin.Context) {
	logLevel := c.Query("level")
	if logLevel == "" {
		NewParamsErrorResponse(c, "level is required")
		return
	}
	// NOTE: 管理员使用，忽略具体值的校验
	logger.SetLoggerLevel(logLevel)
	NewSuccessResponse(c, nil)
}
