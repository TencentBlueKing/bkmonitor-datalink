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
	serviceWorkerListenPath = "service.worker.listen"
	serviceWorkerPortPath   = "service.worker.port"
)

func init() {
	viper.SetDefault(serviceWorkerListenPath, "127.0.0.1")
	viper.SetDefault(serviceWorkerPortPath, 10212)
	// add subcommand
	rootCmd.AddCommand(workerCmd)
}

var workerCmd = &cobra.Command{
	Use:   "worker",
	Short: "bk monitor workers",
	Long:  "worker module for blueking monitor worker",
	Run:   startWroker,
}

// start 启动服务
func startWroker(cmd *cobra.Command, args []string) {
	fmt.Println("start worker service...")
	// 初始化配置
	config.InitConfig()

	// 初始化日志
	logging.InitLogger()

	ctx, cancel := context.WithCancel(context.Background())

	// 启动 worker
	workerSvr, err := service.NewWorkerService()
	if err != nil {
		logger.Fatalf("start worker error, %v", err)
	}

	// start http service, not include api router
	r := service.NewHTTPService(false)
	srv := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", viper.GetString(serviceWorkerListenPath), viper.GetInt(serviceWorkerPortPath)),
		Handler: r,
	}
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
			workerSvr.Shutdown()
			if err := srv.Shutdown(ctx); err != nil {
				logger.Fatalf("shutdown worker service error : %s", err)
			}
			logger.Warn("worker service exit by syscall SIGQUIT, SIGTERM or SIGINT")
			return
		}
	}
}
