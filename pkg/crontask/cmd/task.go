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
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/crontask/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/crontask/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var rootCmd = &cobra.Command{
	Use:   "cron-task",
	Short: "celery cron task module for bk-monitor",
	Long:  "celery cron task module for bk-monitor",
	Run:   start,
}

// start 启动服务
func start(cmd *cobra.Command, args []string) {
	fmt.Println("start service...")

	config.InitConfig()
	s := make(chan os.Signal)
	signal.Notify(s, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)

	for {
		// 加载依赖及启动 worker
		cli := startService()
		svr := startHttpService()

		switch <-s {
		case syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT:
			storage.GlobalDBSession.Close()
			storage.GlobalRedisSession.Close()
			cli.StopWorker()
			svr.Shutdown(context.TODO())
			logger.Warn("service exit by syscall SIGQUIT, SIGTERM or SIGINT")
			return
		}
	}
}

// Execute 执行命令
func Execute() {
	// cobra.OnInitialize(config.InitConfig)
	rootCmd.Flags().StringVarP(
		&config.ConfigPath, "config", "c", "", "path of project service config files",
	)
	if err := rootCmd.Execute(); err != nil {
		fmt.Printf("start cron-task service error, %s", err)
		os.Exit(1)
	}
}
