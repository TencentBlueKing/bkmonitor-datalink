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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/filewatcher"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Start app as operator mode",
	Long:  `Operator lists/watches resources from kubernetes then dispatches tasks to workers`,
	Run: func(cmd *cobra.Command, args []string) {
		waitUntil, err := filewatcher.AddPath(define.ConfigFilePath)
		if err != nil {
			logger.Fatalf("watch config file %s failed: %s", define.ConfigFilePath, err)
		}
		defer filewatcher.Stop()

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

		logger.Infof("loading config %s", define.ConfigFilePath)
		<-waitUntil
		logger.Infof("loaded config %s finished", define.ConfigFilePath)

		if err := configs.Load(define.ConfigFilePath); err != nil {
			logger.Fatalf("failed to load config %s: %s", define.ConfigFilePath, err)
		}

		var reloadTotal int
	Outer:
		for {
			ctx, cancel := context.WithCancel(context.Background())
			mgr, err := operator.New(ctx, operator.BuildInfo{
				Version: Version,
				GitHash: GitHash,
				Time:    BuildTime,
			})
			if err != nil {
				logger.Fatalf("create operator failed: %s", err)
			}

			if err = mgr.Run(); err != nil {
				logger.Fatalf("run operator failed: %s", err)
			}

			for {
				select {
				case <-waitUntil:
					reloadTotal++
					// 运行过程中重载配置出现错误 则忽略
					logger.Infof("reload operator count: %d", reloadTotal)
					if err := configs.Load(define.ConfigFilePath); err != nil {
						logger.Warnf("[ignore] failed to load config: %v", err)
						continue
					}

					cancel()
					mgr.Stop()
					goto Outer

				case <-sigChan:
					cancel()
					mgr.Stop()
					return
				}
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(runCmd)
}
