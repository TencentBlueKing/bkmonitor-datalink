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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/cache"
	"github.com/google/gops/agent"
	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/featureFlag"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/tsdb"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "run",
	Short: "start unify-query module for bk-monitor",
	Long:  `start unify-query module for bk-monitor`,
	Run: func(cmd *cobra.Command, args []string) {
		var (
			serviceList     []define.Service
			ctx, cancelFunc = context.WithCancel(context.Background())
			sc              = make(chan os.Signal, 1)
		)
		config.InitConfig()

		ctx = metadata.InitHashID(ctx)

		// 启动 gops
		if err := agent.Listen(agent.Options{}); err != nil {
			log.Warnf(ctx, err.Error())
		}

		// 初始化启动任务
		serviceList = []define.Service{
			&consul.Service{},
			&redis.Service{},
			&trace.Service{},
			&influxdb.Service{},
			&tsdb.Service{},
			&promql.Service{},
			&http.Service{},
			&featureFlag.Service{},
			&cache.Service{},
		}
		log.Infof(ctx, "http service started.")

		// 注册信号（重载配置文件 & 停止）
		signal.Notify(sc, syscall.SIGUSR1, syscall.SIGTERM, syscall.SIGINT)
	LOOP:
		for {
			for _, service := range serviceList {
				service.Reload(ctx)
			}
			log.Infof(ctx, "reload done")
			switch <-sc {
			case syscall.SIGUSR1:
				// 触发配置重载动作
				config.InitConfig()
				log.Debugf(ctx, "SIGUSR1 signal got, will reload server")
			case syscall.SIGTERM, syscall.SIGINT:
				log.Debugf(ctx, "shutdown signal got, will shutdown server")
				cancelFunc()
				log.Warnf(ctx, "shutdown signal process done")
				break LOOP
			}
		}
		log.Debugf(ctx, "loop break, wait for all service exit.")
		for _, service := range serviceList {
			log.Warnf(ctx, "close service:%s", service.Type())
			service.Close()
			log.Warnf(ctx, "waiting for service:%s", service.Type())

			service.Wait()
			log.Warnf(ctx, "waiting for service:%s done", service.Type())
		}

		log.Debugf(ctx, "all service exit, server exit now.")
		os.Exit(0)
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// init 加载默认配置
func init() {
	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.
	rootCmd.PersistentFlags().StringVar(
		&config.CustomConfigFilePath, "config", "", "config file (default is $HOME/config.yaml)",
	)

	// Cobra also supports local flags, which will only run
	// when this action is called directly.
	rootCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
