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
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/instance"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/policy/stores/shard"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/service/influxdb"
	traceservice "github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/service/trace"
)

// queryCmd represents the query command
var queryCmd = &cobra.Command{
	Use:   "query",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Run: func(cmd *cobra.Command, args []string) {
		serviceCtx := context.Background()
		serviceCtx, serviceCancel := context.WithCancel(serviceCtx)
		defer serviceCancel()

		// 加载 config 配置
		err := config.InitConfigPath()
		if err != nil {
			exit(1, fmt.Sprintf("config server init error: %s", err.Error()))
		}

		logger := log.NewLogger()

		traceService := traceservice.Service{}
		traceService.Start(serviceCtx)
		if err != nil {
			logger.Errorf(serviceCtx, "trace server start error: %s", err)
			exit(1, err.Error())
		}

		redisCli, err := redis.NewRedis(serviceCtx)
		// redis 强依赖，如果 redis 连接失败则直接报错
		if err != nil {
			logger.Errorf(serviceCtx, "redis server start error: %s", err)
			exit(1, err.Error())
		}

		// 加载操作实例
		err = instance.RegisterInstances(logger)
		if err != nil {
			logger.Errorf(serviceCtx, "instance register error: %s", err)
			exit(1, err.Error())
		}

		serviceName := viper.GetString(redis.ServiceNameConfigPath)
		host := viper.GetString(config.QueryHttpHostConfigPath)
		port := viper.GetInt(config.QueryHttpPortConfigPath)
		dir := viper.GetString(config.QueryHttpDIrConfigPath)
		timeout := viper.GetDuration(config.QueryHttpReadTimeoutConfigPath)
		metric := viper.GetString(config.QueryHttpMetricConfigPath)

		address := fmt.Sprintf("%s:%d", host, port)
		md := metadata.NewMetadata(redisCli, serviceName, logger)
		// 启动 http 服务
		ser, err := influxdb.NewService(
			serviceCtx, address, logger, timeout, dir, metric,
			func(ctx context.Context, clusterName, tagKey, tagValue, db, rp string, start, end int64) ([]*shard.Shard, error) {
				return md.GetShardsByTimeRange(ctx, clusterName, tagKey, tagValue, db, rp, start, end)
			},
		)
		if err != nil {
			logger.Errorf(serviceCtx, "influxdb server start error: %s", err)
			exit(1, err.Error())
		}
		err = ser.Open()
		if err != nil {
			logger.Errorf(serviceCtx, "influxdb server open error: %s", err)
			exit(1, err.Error())
		}

		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL, syscall.SIGHUP)
		for {
			sig := <-quit
			switch sig {
			case syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL:
				{
					logger.Infof(serviceCtx, "shutdown signal got, will shutdown server")
					ser.Close()
					return
				}
			case syscall.SIGHUP:
				{
					logger.Infof(serviceCtx, "shutdown signal got, will shutdown server")
					ser.Close()
					ser.Open()
					return
				}
			default:
				{
					logger.Infof(serviceCtx, fmt.Sprintf("unknown signal catched,signal:%v", sig))
					continue
				}
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(queryCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// queryCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// queryCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
