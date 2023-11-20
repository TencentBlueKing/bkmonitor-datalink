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
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/log"
	service "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/service"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/service/scheduler/daemon"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/runtimex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	// add subcommand
	rootCmd.AddCommand(workerCmd)
	addFlag("worker.queues", "queues", func() {
		rootCmd.PersistentFlags().StringSliceVar(
			&config.WorkerQueues, "queues", config.WorkerQueues, "Specify the queues that worker listens to.",
		)
	})
}

var workerCmd = &cobra.Command{
	Use:   "worker",
	Short: "bk monitor workers",
	Long:  "worker module for blueking monitor worker",
	Run:   startWorker,
}

// start 启动服务
func startWorker(cmd *cobra.Command, args []string) {
	defer runtimex.HandleCrash()

	config.InitConfig()
	log.InitLogger()

	ctx, cancel := context.WithCancel(context.Background())

	// 1. 启动worker服务
	workerService, err := service.NewWorkerService(ctx, config.WorkerQueues)
	if err != nil {
		logger.Fatalf(err.Error())
	}
	go workerService.Run()

	// 2. 启动常驻任务维护器
	daemonTaskMaintainer := daemon.NewDaemonTaskRunMaintainer(ctx, workerService.GetWorkerId())
	go daemonTaskMaintainer.Run()

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
