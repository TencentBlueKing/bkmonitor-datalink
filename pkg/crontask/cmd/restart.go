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
	"strconv"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/crontask/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/crontask/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/crontask/utils/runtimex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/crontask/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// restartCmd restart the service
var restartCmd = &cobra.Command{
	Use:   "restart",
	Short: "restart the service",
	Run:   restart,
}

func restart(cmd *cobra.Command, args []string) {
	// 发送停止信号worker
	pid := os.Getpid()
	allPids, err := runtimex.GetPidByServiceName(config.ServiceName)
	if err != nil {
		fmt.Printf("get pid error: %s", err)
		os.Exit(1)
	}
	// 移除当前环境的进程
	pids := slicex.RemoveItem(allPids, strconv.Itoa(pid))
	if len(pids) == 0 {
		fmt.Printf("not found exist service pid")
		os.Exit(1)
	}
	for _, p := range pids {
		pidInt, _ := strconv.Atoi(p)
		syscall.Kill(pidInt, syscall.SIGTERM)
	}
	// 关闭连接
	storage.GetDBSession().Close()
	storage.GetRedisSession().Close()

	// 重新加载
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

func init() {
	rootCmd.AddCommand(restartCmd)
}
