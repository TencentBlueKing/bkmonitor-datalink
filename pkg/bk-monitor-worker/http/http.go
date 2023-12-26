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
	"github.com/gin-contrib/pprof"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
)

func prometheusHandler() gin.HandlerFunc {
	// 需要使用 go 自带的指标获取 goroutines 数量 
	ph := promhttp.Handler()

	return func(c *gin.Context) {
		ph.ServeHTTP(c.Writer, c.Request)
	}
}

// NewHTTPService new a http service
func NewHTTPService() *gin.Engine {
	svr := NewProfHttpService()

	// 注册任务
	svr.POST(CreateTaskPath, CreateTask)
	// 获取运行中的任务列表
	svr.GET(ListTaskPath, ListTask)
	// 删除任务
	svr.DELETE(DeleteTaskPath, RemoveTask)
	// 删除所有任务
	svr.DELETE(DeleteAllTaskPath, RemoveAllTask)

	return svr
}

// NewProfHttpService new a pprof service
func NewProfHttpService() *gin.Engine {
	svr := gin.Default()
	gin.SetMode(config.GinMode)

	// metrics
	svr.GET("/bmw/metrics", prometheusHandler())

	pprof.Register(svr)

	return svr
}
