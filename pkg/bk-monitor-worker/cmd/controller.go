// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/logging"
	service "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/service"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	serviceControllerListenPath = "service.controller.listen"
	serviceControllerPortPath   = "service.controller.port"
)

func init() {
	viper.SetDefault(serviceControllerListenPath, "127.0.0.1")
	viper.SetDefault(serviceControllerPortPath, 10213)
	// add subcommand
	rootCmd.AddCommand(controllerCmd)
}

var controllerCmd = &cobra.Command{
	Use:   "controller",
	Short: "bk monitor worker controller",
	Long:  "worker module for blueking monitor worker",
	Run:   startController,
}

// start 启动服务
func startController(cmd *cobra.Command, args []string) {
	fmt.Println("start controller service...")
	// 初始化配置
	config.InitConfig()

	// 初始化日志
	logging.InitLogger()

	ctx, cancel := context.WithCancel(context.Background())

	// 启动 controller
	err := service.NewController()
	if err != nil {
		logger.Fatalf("start controller error, %v", err)
	}

	// start http service, not include api router
	r := service.NewHTTPService(false)
	host := viper.GetString(serviceControllerListenPath)
	port := viper.GetInt(serviceControllerPortPath)
	srv := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", host, port),
		Handler: r,
	}

	logger.Infof("controller http service with host: %s and port: %d", host, port)

	go func() {
		// 服务连接
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatalf("listen addr error, %v", err)
		}
	}()

	// 信号处理
	s := make(chan os.Signal)
	signal.Notify(s, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
	for {
		switch <-s {
		case syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT:
			defer cancel()
			if err := srv.Shutdown(ctx); err != nil {
				logger.Fatalf("shutdown controller service error : %s", err)
			}
			logger.Warn("controller service exit by syscall SIGQUIT, SIGTERM or SIGINT")
			return
		}
	}
}
