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
	"sync"
	"time"

	"github.com/panjf2000/ants/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	oleltrace "go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/instance"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/policy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/policy/stores"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/policy/stores/shard"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/redis"
	traceservice "github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/service/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/trace"
)

// moveCmd represents the move command
var moveCmd = &cobra.Command{
	Use:   "move",
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
			exit(1, err.Error())
		}

		logger := log.NewLogger()

		traceService := traceservice.Service{}
		traceService.Start(serviceCtx)
		if err != nil {
			exit(1, err.Error())
		}

		redisCli, err := redis.NewRedis(serviceCtx)
		// redis 强依赖，如果 redis 连接失败则直接报错
		if err != nil {
			exit(1, err.Error())
		}

		// 加载操作实例
		err = instance.RegisterInstances(logger)
		if err != nil {
			logger.Errorf(serviceCtx, "instance register error: %s", err)
			exit(1, err.Error())
		}

		serviceName := viper.GetString(redis.ServiceNameConfigPath)

		instanceName := viper.GetString(config.MoveInstanceNameConfigPath)
		clusterName := viper.GetString(config.MoveClusterNameConfigPath)
		tagName := viper.GetString(config.MoveTagNameConfigPath)
		tagValue := viper.GetString(config.MoveTagValueConfigPath)
		sourceDir := viper.GetString(config.MoveSourceDirConfigPath)
		targetName := viper.GetString(config.MoveTargetNameConfigPath)
		targetDir := viper.GetString(config.MoveTargetDirConfigPath)
		interval := viper.GetDuration(config.MoveIntervalConfigPath)
		maxPool := viper.GetInt(config.MoveMaxPoolConfigPath)
		distributeLockExpiration := viper.GetDuration(config.MoveDistributedLockExpiration)
		distributeLockRenewalDuration := viper.GetDuration(config.MoveDistributedLockRenewalDuration)

		address := viper.GetString(config.MoveInfluxDBAddressConfigPath)
		username := viper.GetString(config.MoveInfluxDBUserNameConfigPath)
		password := viper.GetString(config.MoveInfluxDBPasswordConfigPath)

		md := metadata.NewMetadata(redisCli, serviceName, logger)
		influxdb := stores.NewInfluxDB(
			logger, clusterName, instanceName, tagName, tagValue,
			sourceDir, targetName, targetDir, address, username, password,
		)

		logger.Infof(serviceCtx, "service start with: %+v", viper.AllSettings())

		// 定义move执行方法
		p, _ := ants.NewPoolWithFunc(maxPool, func(i interface{}) {
			invoke, ok := i.(*invoke)
			if ok {
				ctx := invoke.ctx
				sd := invoke.sd
				defer invoke.wg.Done()

				if sd == nil {
					logger.Errorf(ctx, "shard is nil")
					return
				}

				// 只有状态为移动和丢弃的才会被进入备份流程
				if sd.Status.Code != shard.Move {
					logger.Debugf(ctx,
						"[root] find this shard status code not move, skip archive this shard, shard: %s status_code: %+v",
						sd.Unique(), sd.Status.Code,
					)
					return
				}
				err := sd.Run(ctx, nil,
					func(ctx context.Context, key, val string) (string, error) {
						return md.GetDistributedLock(ctx, key, val, distributeLockExpiration)
					},
					func(ctx context.Context, key string) (bool, error) {
						// 续期
						return md.RenewalLock(ctx, key, distributeLockRenewalDuration)
					},
					func(ctx context.Context, key string, shard *shard.Shard) error {
						// update
						logger.Infof(ctx, "update shard[%s] => %s", shard.Unique())
						err := md.SetShard(ctx, key, shard)
						if err == nil {
							logger.Infof(ctx, "publish shard to rebuild module=>%s", shard.Unique())
							err = md.PublishShard(ctx, key)
						}
						return err
					},
				)
				if err != nil {
					logger.Errorf(ctx, "shard %s run error %s", sd.Unique(), err.Error())
					return
				}
			} else {
				logger.Errorf(serviceCtx, "shard type is error: %+v", i)
				return
			}
		}, ants.WithPreAlloc(true))
		defer p.Release()

		ticker := time.NewTicker(interval)
		defer func() {
			ticker.Stop()
		}()

		for {
			select {
			case <-serviceCtx.Done():
				return
			case <-ticker.C:
				func() {
					var (
						span oleltrace.Span
						wg   sync.WaitGroup
					)

					ctx, cancel := context.WithCancel(context.Background())
					if cancel != nil {
						defer cancel()
					}

					ctx, span = trace.IntoContext(ctx, trace.TracerName, "move-ticker-run")
					if span != nil {
						defer span.End()
					}

					logger.Infof(ctx, "move-ticker-run running check %s %s %s\n", instanceName, tagName, tagValue)
					policies, err := md.GetPolicies(ctx, clusterName, tagName, tagValue)

					trace.InsertIntIntoSpan("policies-num", len(policies), span)
					if err != nil {
						logger.Errorf(ctx, "get policies error: %s", err.Error())
						return
					}

					logger.Infof(ctx, "found %d policy need to run", len(policies))
					for _, pv := range policies {
						meta := &policy.Meta{
							Name:        instanceName,
							ClusterName: pv.ClusterName,
							TagName:     pv.TagName,
							TagValue:    pv.TagValue,
							Database:    pv.Database,
						}

						logger.Infof(ctx, "check policy %s %s %s %s", pv.ClusterName, pv.TagName, pv.TagValue, pv.Database)
						mdShards, err := md.GetShards(ctx, pv.ClusterName, pv.TagName, pv.TagValue, pv.Database)

						if err != nil {
							logger.Errorf(ctx, "get shards error: %s", err.Error())
							continue
						}

						logger.Infof(ctx, "find %d shards from metadata", len(mdShards))
						policyObject := policy.NewPolicy(
							ctx, meta, influxdb, func() bool {
								return pv.Enable
							}, logger,
						)

						activeShards := policyObject.GetActiveShards(ctx, mdShards)
						logger.Infof(ctx, "find %d archive shards need archive", len(activeShards))

						for _, sd := range activeShards {
							wg.Add(1)
							err = p.Invoke(&invoke{
								ctx: ctx,
								wg:  &wg,
								sd:  sd,
							})
							if err != nil {
								logger.Errorf(ctx, "shard invoke: %s", err.Error())
							}
						}

						wg.Wait()
					}
				}()

			}

		}
	},
}

func init() {
	rootCmd.AddCommand(moveCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// moveCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// moveCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
