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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/api-server/apis/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/api-server/apis/plugincollect"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/api-server/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/api-server/metrics"
)

// NewHTTPService new a http service
func NewHTTPService() *gin.Engine {
	svr := newProfHttpService()

	// 路由配置
	router := svr.Group(PathPrefix)
	// 添加中间件
	router.Use(MetricMiddleware())
	router.Use(ResponseMiddleware())

	// 注册路由
	addApiRouter(router)
	// 注册插件采集路由
	addCollectRouter(router)

	return svr
}

// addApiRouter add api router
func addApiRouter(router *gin.RouterGroup) {
	// 查询指标
	router.GET(MetricsPath, prometheusHandler())
	// 动态设置日志级别
	router.PUT(LogLevelPath, log.SetLogLevel)
}

// addCollectRouter add collect api router
func addCollectRouter(router *gin.RouterGroup) {
	router = router.Group(CollectPrefixPath).Group(PluginCollectPrefixPath)
	// watch
	router.GET(PluginCollectWatchPath, plugincollect.Watch)
}

// newProfHttpService new a pprof service
func newProfHttpService() *gin.Engine {
	svr := gin.Default()
	svr.Use(gin.Recovery())
	gin.SetMode(config.Config.Http.Mode)

	pprof.Register(svr)

	return svr
}

func prometheusHandler() gin.HandlerFunc {
	// 需要使用 go 自带的指标获取 goroutines 数量
	gatherers := &prometheus.Gatherers{
		prometheus.DefaultGatherer, // 默认的数据采集器，包含go运行时的指标信息
		metrics.Registry,           // 自定义的采集器
	}
	ph := promhttp.InstrumentMetricHandler(
		metrics.Registry, promhttp.HandlerFor(gatherers, promhttp.HandlerOpts{}),
	)

	return func(c *gin.Context) {
		ph.ServeHTTP(c.Writer, c.Request)
	}
}
