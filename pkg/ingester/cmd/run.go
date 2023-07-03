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
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/datasource"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/http"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/poller"
)

var service *consul.Service

// runCmd represents the run command
var runCmd = &cobra.Command{
	Use:       "run [all,receiver,poller]",
	Short:     "Run ingester server",
	Long:      `Run ingester server for receiving and polling event data`,
	Args:      cobra.OnlyValidArgs,
	ValidArgs: []string{"all", "receiver", "poller"},
	Run: func(cmd *cobra.Command, args []string) {
		go http.RunServer()
		waitForSignal()
	},
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		cobra.CheckErr(config.Init())
		logging.Init()
		define.ServiceID = define.GetServiceID()
	},
	PreRunE: func(cmd *cobra.Command, args []string) error {
		err := setupPid(pidFile, !startSafety)
		if err != nil {
			return err
		}
		initServerConfig()

		logger := logging.GetLogger()

		runMode := "all"
		if len(args) > 0 {
			runMode = args[0]
		}
		var tags []string
		switch runMode {
		case "receiver":
			datasource.RegisterDataSourceSubscriber("receiver", http.Subscriber)
			tags = []string{"receiver"}
			logger.Infof("run mode: receiver")
		case "poller":
			datasource.RegisterDataSourceSubscriber("poller", poller.Subscriber)
			tags = []string{"poller"}
			logger.Infof("run mode: poller")
		default:
			datasource.RegisterDataSourceSubscriber("receiver", http.Subscriber)
			datasource.RegisterDataSourceSubscriber("poller", poller.Subscriber)
			tags = []string{"receiver", "poller"}
			logger.Infof("run mode: all")
		}

		// 将服务注册到 consul
		service, err = consul.NewService(tags)
		if err != nil {
			return err
		}
		err = service.Start()
		if err != nil {
			return err
		}

		go datasource.StartWatchDataSource()

		return nil
	},
	PostRunE: func(cmd *cobra.Command, args []string) error {
		// 停止配置监听
		datasource.StopWatchDataSource()

		// 注销consul
		if service != nil {
			err := service.Stop()
			if err != nil {
				return nil
			}
		}

		tearDownPid(pidFile)
		return nil
	},
}

func waitForSignal() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	logger := logging.GetLogger()
	for sig := range signals {
		logger.Infof("signal %v received", sig)
		switch sig {
		case syscall.SIGINT, syscall.SIGTERM:
			return
		}
	}
}

func setupPid(pidPath string, force bool) error {
	logger := logging.GetLogger()
	_, err := os.Stat(pidPath)
	if err == nil {
		if force {
			logger.Warnf("remove pid file %s", pidPath)
			err = os.RemoveAll(pidPath)
			if err != nil {
				logger.Errorf("remove pid file %s error: %v", pidPath, err)
			}
		} else {
			return fmt.Errorf("pid file %s already exists", pidPath)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	return os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644)
}

func tearDownPid(pidPath string) {
	logger := logging.GetLogger()
	err := os.RemoveAll(pidPath)
	if err != nil {
		logger.Warnf("remove pid file %s error: %v", pidPath, err)
	}
}

// debug: 是否启用 debug 模式
var debug bool

// pidFile: PID 文件路径
var pidFile string

var startSafety bool

func init() {
	rootCmd.AddCommand(runCmd)

	flags := runCmd.Flags()

	flags.BoolVar(&debug, "debug", false, "Use debug mode")
	flags.StringVarP(&pidFile, "pid", "p", "ingester.pid", "pid file")
	flags.BoolVar(&startSafety, "safety", false, "start safety")
}

func initServerConfig() {
	// 配置优先级：命令行参数 > 配置文件
	// 若配置了命令行参数，此处将配置覆盖∂
	if debug {
		config.Configuration.Http.Debug = debug
	}
}
