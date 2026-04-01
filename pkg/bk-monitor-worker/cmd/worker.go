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
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	bmwHttp "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/http"
	bmwRelation "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/relation"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/log"
	service "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/service"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/service/scheduler/daemon"
	bmwRedis "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/runtimex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
	goRedis "github.com/go-redis/redis/v8"
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
	// 初始化日志
	log.InitLogger()

	ctx, cancel := context.WithCancel(context.Background())

	pm := relation.NewProviderManager(nil)

	var redisClient goRedis.UniversalClient
	if config.SchemaProviderType == "redis" {
		inst := bmwRedis.GetStorageRedisInstance()
		if inst == nil || inst.Client == nil {
			logger.Errorf("[schema_provider] redis instance not ready, type=%s", config.SchemaProviderType)
			cancel()
			return
		}
		redisClient = inst.Client
	}

	if err := pm.InitProvider(ctx, config.SchemaProviderType, redisClient); err != nil {
		// Graceful degradation: log warning but continue with nil provider
		// This allows base metrics to still be reported even if relation metadata provider fails
		logger.Warnf("[schema_provider] init failed, degrading gracefully: %v", err)
	}

	// InitSchemaProvider accepts nil provider (falls back to hardcoded config)
	bmwRelation.InitSchemaProvider(pm.GetProvider())
	logger.Infof("[schema_provider] initialized with type=%s", config.SchemaProviderType)

	r := bmwHttp.NewProfHttpService()

	srv := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", config.WorkerListenHost, config.WorkerListenPort),
		Handler: r,
	}
	logger.Infof("Starting HTTP server at %s:%d", config.WorkerListenHost, config.WorkerListenPort)

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatalf("listen addr error, %v", err)
		}
	}()

	// 1. 启动worker服务
	workerService, err := service.NewWorkerService(ctx, config.WorkerQueues)
	if err != nil {
		logger.Fatalf(err.Error())
	}
	go workerService.Run()

	// 2. 启动常驻任务维护器
	daemonTaskMaintainer := daemon.NewDaemonTaskRunMaintainer(ctx, workerService.GetWorkerId())
	go daemonTaskMaintainer.Run()

	s := make(chan os.Signal, 1)
	signal.Notify(s, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
	for {
		switch <-s {
		case syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT:
			workerService.Stop()
			_ = pm.Close()
			cancel()
			srv.Close()
			logger.Info("Bye")
			os.Exit(0)
		}
	}
}
