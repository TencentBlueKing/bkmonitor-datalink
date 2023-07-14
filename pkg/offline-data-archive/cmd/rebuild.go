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
	"path/filepath"
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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/policy/stores/shard"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/redis"
	traceservice "github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/service/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/trace"
)

// rebuildCmd represents the rebuild command
var rebuildCmd = &cobra.Command{
	Use:   "rebuild",
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
		logger := log.NewLogger()

		// 加载 config 配置
		err := config.InitConfigPath()
		if err != nil {
			exit(1, err.Error())
		}

		traceService := traceservice.Service{}
		traceService.Start(serviceCtx)
		if err != nil {
			logger.Errorf(serviceCtx, "trace server open error: %s", err)
			exit(1, err.Error())
		}

		redisCli, err := redis.NewRedis(serviceCtx)
		// redis 强依赖，如果 redis 连接失败则直接报错
		if err != nil {
			logger.Errorf(serviceCtx, "redis server open error: %s", err)
			exit(1, err.Error())
		}

		// 加载操作实例
		err = instance.RegisterInstances(logger)
		if err != nil {
			logger.Errorf(serviceCtx, "instance register error: %s", err)
			exit(1, err.Error())
		}

		serviceName := viper.GetString(redis.ServiceNameConfigPath)
		interval := viper.GetDuration(config.RebuildIntervalConfigPath)
		maxPool := viper.GetInt(config.RebuildMaxPoolConfigPath)
		distributeLockExpiration := viper.GetDuration(config.RebuildDistributedLockExpirationConfigPath)
		distributeLockRenewalDuration := viper.GetDuration(config.RebuildDistributedLockRenewalDurationConfigPath)
		finalName := viper.GetString(config.RebuildFinalNameConfigPath)
		finalDir := viper.GetString(config.RebuildFinalDirConfigPath)
		tempDir := viper.GetString(config.CommonTempDirConfigPath)

		md := metadata.NewMetadata(redisCli, serviceName, logger)

		logger.Infof(serviceCtx, "service start with: %+v", viper.AllSettings())

		shardCh := md.SubscribeShard(serviceCtx)
		todoCh := make(chan *shard.Shard, maxPool)
		defer close(todoCh)

		// 定义rebuild方法提前申请内存
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

				// 只有状态为 Rebuild 的才会被进入备份流程
				if sd.Status.Code != shard.Rebuild {
					logger.Debugf(ctx,
						"[root] find this shard status code not rebuild, skip archive this shard, shard: %s status_code: %s",
						sd.Unique(), sd.CodeName(),
					)
					return
				}

				// 获取全局唯一 shardID
				shardID, err := md.GetShardID(ctx, sd)
				if err != nil {
					logger.Errorf(ctx, err.Error())
					return
				}

				// 注入 finalDir 信息
				finalPath := filepath.Join(finalDir, shardID)
				sd.Spec.Final = shard.Instance{
					InstanceType: instance.CosName,
					Name:         finalName,
					ShardID:      shardID,
					Path:         finalPath,
				}

				baseAction := &shard.BaseAction{
					TempDir: tempDir,
				}

				err = sd.Run(ctx, baseAction,
					func(ctx context.Context, key, val string) (string, error) {
						return md.GetDistributedLock(ctx, key, val, distributeLockExpiration)
					},
					func(ctx context.Context, key string) (bool, error) {
						// 续期
						return md.RenewalLock(ctx, key, distributeLockRenewalDuration)
					},
					func(ctx context.Context, key string, shard *shard.Shard) error {
						// update
						logger.Infof(ctx, "start update shard=>%s", shard.Unique())
						return md.SetShard(ctx, key, shard)
					},
				)
				if err != nil {
					// 如果执行异常问题，则重新加入到待处理 chan 里
					logger.Errorf(ctx, "rebuild shard error %s", err.Error())
					todoCh <- sd
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
					defer func() {
						wg.Wait()
						if cancel != nil {
							cancel()
						}
					}()

					ctx, span = trace.IntoContext(ctx, trace.TracerName, "rebuild-ticker-run")
					if span != nil {
						defer span.End()
					}
					shards := md.GetAllShards(ctx)
					logger.Infof(ctx, "rebuild-ticker-run found %d shard need to run", len(shards))
					trace.InsertIntIntoSpan("shard-num", len(shards), span)
					for _, sd := range shards {
						wg.Add(1)
						p.Invoke(&invoke{
							ctx: ctx,
							wg:  &wg,
							sd:  sd,
						})
					}
				}()
			case msg := <-shardCh:
				if msg != nil {
					func() {
						var (
							span oleltrace.Span
							wg   sync.WaitGroup
						)
						ctx, cancel := context.WithCancel(context.Background())
						defer func() {
							wg.Wait()
							if cancel != nil {
								cancel()
							}
						}()
						// 获取shardKey
						logger.Infof(ctx, "receive a message, message:%+v", msg)
						sd, err := md.GetShard(ctx, msg.Payload)
						if err != nil {
							logger.Errorf(ctx, "get shard error: %s", err.Error())
							return
						}

						ctx, span = trace.IntoContext(ctx, trace.TracerName, "rebuild-sub")
						if span != nil {
							defer span.End()
						}

						wg.Add(1)
						p.Invoke(&invoke{
							ctx: ctx,
							wg:  &wg,
							sd:  sd,
						})
					}()
				}
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(rebuildCmd)
}
