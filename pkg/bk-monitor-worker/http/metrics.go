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

	"github.com/gin-contrib/pprof"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/relation"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
)

// NewProfHttpService new a pprof service
func NewProfHttpService() *gin.Engine {
	svr := gin.Default()
	svr.Use(gin.Recovery())
	gin.SetMode(config.GinMode)

	// metrics
	svr.GET("/bmw/metrics", prometheusHandler())
	svr.GET("/bmw/relation/metrics", relationHandler)
	svr.GET("/bmw/relation/debug", func(c *gin.Context) {
		bizID := c.Query("biz_id")

		result := relation.GetRelationMetricsBuilder().Debug(bizID)

		c.String(http.StatusOK, result)
	})

	pprof.Register(svr)

	// 动态设置日志级别
	svr.POST("/bmw/log/level", SetLogLevel)
	return svr
}

func relationHandler(c *gin.Context) {
	relationMetrics := relation.GetRelationMetricsBuilder().String()

	c.String(http.StatusOK, relationMetrics)
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

// addMetricMiddleware add metric middleware
func addMetricMiddleware(svr *gin.Engine) {
	svr.Use(func(c *gin.Context) {
		startTime := time.Now()
		c.Next()
		status := c.Writer.Status()
		method, reqPath := c.Request.Method, c.Request.URL.Path
		// NOTE: 20x 和 30x 都是成功场景，其它为失败场景
		if status >= http.StatusOK && status < http.StatusBadRequest {
			metrics.RequestApiTotal(method, reqPath, "success")
			metrics.RequestApiCostTime(c.Request.Method, c.Request.URL.Path, startTime)
		} else {
			metrics.RequestApiTotal(method, reqPath, "failure")
		}
	})
}
