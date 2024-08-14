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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/filewatcher"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/reloader"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var reloaderCmd = &cobra.Command{
	Use:   "reloader",
	Short: "Start app as reloader mode",
	Long:  "Reloader watches configs then send signal to worker",
	Run: func(cmd *cobra.Command, args []string) {
		waitUntil, err := filewatcher.AddPath(config.CustomConfigFilePath)
		if err != nil {
			logger.Fatalf("watch config file '%s' failed: %s", config.CustomConfigFilePath, err)
		}
		defer filewatcher.Stop()

		logger.Infof("waiting file '%s' to be updated", config.CustomConfigFilePath)
		<-waitUntil
		logger.Info("reloader is ready to worker")

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

		if err = config.InitConfig(); err != nil {
			logger.Fatalf("failed to load config: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		rdr, err := reloader.NewReloader(ctx)
		if err != nil {
			logger.Fatalf("crate reloader failed, error: %s", err)
		}

		if err = rdr.Run(); err != nil {
			logger.Fatalf("run reloader failed, error: %s", err)
		}

		<-sigChan
		cancel()
		rdr.Stop()
	},
}

func init() {
	rootCmd.AddCommand(reloaderCmd)
}
