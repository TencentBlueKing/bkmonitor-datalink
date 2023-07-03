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
	"net/http"

	"github.com/gocelery/gocelery"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/crontask/broker"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/crontask/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/crontask/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/crontask/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	serviceWorkerNumberPath = "service.worker_number"
	serviceListenPath       = "service.listen"
	servicePortPath         = "service.port"
)

func init() {
	viper.SetDefault(serviceWorkerNumberPath, 2)
	viper.SetDefault(serviceListenPath, "127.0.0.1")
	viper.SetDefault(servicePortPath, 10209)
}

func startService() *gocelery.CeleryClient {
	// init logger
	logging.InitLogger()

	// init DB client
	storage.GetDBSession()
	// init redis client
	storage.GetRedisSession()
	// 启动 worker
	cli, _ := gocelery.NewCeleryClient(
		broker.NewBroker(),
		broker.NewBackend(),
		viper.GetInt(serviceWorkerNumberPath),
	)
	// register task
	tasks.RegisterTasks(cli)
	// start workers (non-blocking call)
	cli.StartWorker()

	return cli
}

func startHttpService() *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	// 配置 host
	host := fmt.Sprintf("%s:%d", viper.GetString(serviceListenPath), viper.GetInt(servicePortPath))
	server := &http.Server{Addr: host, Handler: mux}

	go func() {
		if err := server.ListenAndServe(); err != nil {
			logger.Errorf("start http server error: %v", err)
		}
	}()
	return server
}
