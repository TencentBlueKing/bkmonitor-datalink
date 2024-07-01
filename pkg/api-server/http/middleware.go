// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/api-server/apis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/api-server/metrics"
)

func MetricMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		startTime := time.Now()

		c.Next()

		// 获取状态码
		status := c.Writer.Status()
		method, reqPath := c.Request.Method, c.Request.URL.Path
		// NOTE: 20x 和 30x 都是成功场景，其它为失败场景；失败场景不记录耗时
		if status >= http.StatusOK && status < http.StatusBadRequest {
			metrics.RequestApiTotal(method, reqPath, "success")
			metrics.RequestApiDurationSeconds(c.Request.Method, c.Request.URL.Path, startTime)
		} else {
			metrics.RequestApiTotal(method, reqPath, "failure")
		}
	}
}

// ResponseMiddleware 响应处理中间件
func ResponseMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				// 处理异常情况
				c.JSON(http.StatusInternalServerError, nil)
			}
		}()
		// 执行正常的请求处理逻辑
		c.Next()

		// 获取响应数据
		responseData := c.Keys["response"].(*apis.ApiResponse)
		// 设置响应头
		c.Header("Content-Type", "application/json; charset=utf-8")
		// 发送响应
		c.JSON(responseData.HttpCode, responseData.Resp)
	}
}
