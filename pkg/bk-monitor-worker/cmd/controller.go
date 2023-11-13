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
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/runtimex"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	service "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/http"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	rootCmd.AddCommand(controllerCmd)
}

var controllerCmd = &cobra.Command{
	Use:   "controller",
	Short: "bk monitor worker controller",
	Long:  "worker module for blueking monitor worker",
	Run:   startController,
}

func startController(cmd *cobra.Command, args []string) {
	defer runtimex.HandleCrash()

	config.InitConfig()
	log.InitLogger()

	r := service.NewHTTPService(true)

	srv := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", config.HttpListenPath, config.HttpListenPort),
		Handler: r,
	}
	logger.Infof("Starting HTTP server at %s:%d", config.HttpListenPath, config.HttpListenPort)

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatalf("listen addr error, %v", err)
		}
	}()

	s := make(chan os.Signal)
	signal.Notify(s, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
	for {
		switch <-s {
		case syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT:
			logger.Info("Bye")
			os.Exit(0)
		}
	}
}
