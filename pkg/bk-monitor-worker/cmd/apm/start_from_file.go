// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package apm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	bmwHttp "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/http"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/tools"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var filePath string

func StartFromFileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start_from_file",
		Short: "start apm task from file",
		Run:   listenFile,
	}

	cmd.Flags().StringVarP(&filePath, "file", "f", "", "connection file")
	return cmd
}

func listenFile(cmd *cobra.Command, args []string) {
	config.InitConfig()
	log.InitLogger()

	r := bmwHttp.NewProfHttpService()
	srv := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", config.ControllerListenHost, config.ControllerListenPort),
		Handler: r,
	}
	logger.Infof("Starting HTTP server at %s:%d", config.ControllerListenHost, config.ControllerListenPort)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("listen addr error, %v", err)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	if err := tools.StartListenerFromFile(ctx, filePath); err != nil {
		logger.Fatal(err)
	}
	s := make(chan os.Signal)
	signal.Notify(s, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
	for {
		switch <-s {
		case syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT:
			cancel()
			logger.Infof("Bye")
			os.Exit(0)
		}
	}
}
