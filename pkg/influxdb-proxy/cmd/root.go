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
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend/influxdb"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/cluster/routecluster"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/event"
	bkeventbus "github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/golang/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/golang/utils"
	tshttp "github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/http"
	logger "github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

var cfgFile string

// Execute :
var (
	AppName string
	Mode    string
)

// Execute cmd的执行入口
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", path.Join("config", fmt.Sprintf("%s.yml", AppName)), "config file")
}

var rootCmd = &cobra.Command{
	Use:   "github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy",
	Short: "high-available proxy for influxdb",
	Run: func(cmd *cobra.Command, args []string) {
		// 初始化配置
		initConfigPath()

		// 初始化http模块
		c := common.Config
		mux := http.DefaultServeMux

		service, err := tshttp.NewHTTPService(mux)
		if err != nil {
			logger.StdLogger.Errorf("failed to init service for->[%s]", err.Error())
			panic(err)
		}

		readTimeout := time.Duration(c.GetInt("http.read_timeout"))
		readHeaderTimeout := time.Duration(c.GetInt("http.read_header_timeout"))
		server := http.Server{
			Addr:              fmt.Sprintf("%s:%s", c.GetString("http.listen"), c.GetString("http.port")),
			Handler:           mux,
			ReadTimeout:       readTimeout * time.Second,
			ReadHeaderTimeout: readHeaderTimeout * time.Second,
		}
		logger.StdLogger.Infof("start http service")
		go func() {
			utils.CheckError(server.ListenAndServe())
		}()

		// 开始监听外部信号
		logger.StdLogger.Infof("watch http service stop signal")
		quit := make(chan os.Signal)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL, syscall.SIGHUP)
		for {
			sig := <-quit
			switch sig {
			case syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL:
				{
					logger.StdLogger.Infof("get stop signal:%v,start to shutdown http service", sig)
					service.Shutdown()
					logger.StdLogger.Infof("http service stopped")
					service.Wait()
					return
				}
			case syscall.SIGHUP:
				{
					logger.StdLogger.Infof("get reload signal,start reload service")
					err := service.Reload(0)
					if err != nil {
						logger.StdLogger.Errorf("reload failed,error:%s", err)
						continue
					}
					logger.StdLogger.Infof("reload done")
				}
			default:
				{
					logger.StdLogger.Errorf("unknown signal catched,signal:%v", sig)
					continue
				}
			}
		}
	},
}

// 设置配置读取路径
func initConfigPath() {
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		dir, err := os.Getwd()
		if err != nil {
			utils.PrintWarning("init config error: %+v", err)
			os.Exit(1)
		}

		// Search config in home directory with name (without extension).
		viper.AddConfigPath(path.Join(dir, "config"))
		viper.SetConfigName(AppName)
	}

	v := viper.GetViper()
	v.SetEnvPrefix(AppName)
	v.AutomaticEnv() // read in environment variables that match
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	conf := common.NewViperConfiguration(v)

	bkeventbus.Publish(event.EvSysConfigPreParse, conf)

	if Mode == "debug" {
		viper.Set("debug", true)
	} else {
		viper.SetDefault("debug", false)
	}

	viper.SetDefault("http.read_timeout", 30)
	viper.SetDefault("http.read_header_timeout", 3)
	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		utils.PrintWarning("Using config file:: %s\n", v.ConfigFileUsed())
	} else {
		utils.PrintWarning("cannot find file: %s, exit.\n", v.ConfigFileUsed())
		os.Exit(2)
	}
	bkeventbus.Publish(event.EvSysConfigPostParse, conf)
}
