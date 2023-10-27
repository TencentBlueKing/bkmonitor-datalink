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
	"time"

	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	ginprometheus "github.com/zsais/go-gin-prometheus"
	"go.uber.org/zap"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/logging"
)

func RunServer() {
	logger := logging.GetLogger()

	if config.Configuration.Http.Debug {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	engine := gin.Default()

	// 初始化 Prometheus 配置
	prom := ginprometheus.NewPrometheus("gin")
	prom.Use(engine)

	// 初始化日志配置
	ginLogger := logger.Desugar().WithOptions(zap.AddCallerSkip(6))
	engine.Use(ginzap.Ginzap(ginLogger, time.RFC3339, true))
	engine.Use(ginzap.RecoveryWithZap(ginLogger, true))

	route(engine)

	bindAddress := config.Configuration.Http.GetBindAddress()

	logger.Infof("Listening and serving HTTP on %s", bindAddress)
	err := engine.Run(bindAddress)
	if err != nil {
		logger.Fatal(err)
	}
}
