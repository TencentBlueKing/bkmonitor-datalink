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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/service/scheduler/daemon"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/service/scheduler/periodic"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/runtimex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"github.com/spf13/cobra"
	"os"
	"os/signal"
	"syscall"
)

func init() {
	rootCmd.AddCommand(taskModuleCmd)
}

var taskModuleCmd = &cobra.Command{
	Use:   "task",
	Short: "bk monitor tasks",
	Long:  "task module for blueking monitor worker",
	Run:   startTaskModule,
}

func startTaskModule(cmd *cobra.Command, args []string) {
	defer runtimex.HandleCrash()

	config.InitConfig()
	log.InitLogger()

	ctx, cancel := context.WithCancel(context.Background())

	// 1. 启动任务监听器
	taskWatcher := periodic.NewWatchService(ctx)
	go taskWatcher.StartWatch()

	// 2. 启动周期任务调度器
	periodicTaskScheduler := periodic.NewPeriodicTaskScheduler(ctx)
	go periodicTaskScheduler.Run()

	// 3. 启动常驻任务调度器
	daemonTaskScheduler := daemon.NewDaemonTaskScheduler(ctx)
	go daemonTaskScheduler.Run()

	logger.Infof("Task module started.")
	s := make(chan os.Signal)
	signal.Notify(s, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
	for {
		switch <-s {
		case syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT:
			cancel()
			logger.Info("Bye")
			os.Exit(0)
		}
	}
}
