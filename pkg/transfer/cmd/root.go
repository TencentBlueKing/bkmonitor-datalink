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
	"math"
	"os"
	"path"
	"runtime/trace"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/filesystem"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

var (
	cfgFile     string
	traceFile   filesystem.File
	clusterName string
	host        string
	port        string
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   define.AppName,
	Short: "collect, etl and shipper",
	Long:  `BkMonitor transfer system`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		resourceLimits(cmd)
		setupTraceLog(cmd)
		eventbus.Publish(eventbus.EvRunnerPreRun, cmd.Use)
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		eventbus.Publish(eventbus.EvRunnerPostRun, cmd.Use)
		tearDownTraceLog(cmd)
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		utils.PrintWarning("execute error: %+v\n", err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.
	flags := rootCmd.PersistentFlags()
	flags.StringVarP(&cfgFile, "config", "c", "", "config file")
	flags.String("trace-log", "", "trace log path")
	flags.Float64("max-cpus", math.Inf(1), "maximum number of CPUs that can be scheduling")
	flags.Float64("max-files", math.Inf(1), "maximum number of files that can be opening")
	flags.StringVar(&clusterName, "cluster-name", "", "cluster joined by transfer")
	flags.StringVar(&host, "host", "", "ip address for transfer")
	flags.StringVar(&port, "port", "", "http port by transfer listen")
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
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
		viper.AddConfigPath(dir)
		viper.SetConfigName(define.AppName)
	}

	v := viper.GetViper()
	v.SetEnvPrefix(define.AppName)
	v.AutomaticEnv() // read in environment variables that match
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	conf := define.NewViperConfiguration(v)

	eventbus.Publish(eventbus.EvSysConfigPreParse, conf)

	if define.Mode == "debug" {
		viper.Set("debug", true)
	} else {
		viper.SetDefault("debug", false)
	}

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		utils.PrintWarning("Using config file: %s\n", v.ConfigFileUsed())
	} else {
		utils.PrintWarning("Parse config error %v\n", err)
	}

	// 使用命令行参数取代cluster，http.host  http.port
	pathVersion := conf.GetString(consul.ConfKeyPathVersion)
	switch pathVersion {
	case "":
		conf = parseV0Path(conf)
	default:
		// 默认是用v1版本的路径规则
		conf = parseV1Path(conf)
	}

	eventbus.Publish(eventbus.EvSysConfigPostParse, conf)
}

func setupTraceLog(cmd *cobra.Command) {
	flags := cmd.Flags()
	traceLog, _ := flags.GetString("trace-log")
	if traceLog == "" {
		return
	}

	file, err := filesystem.FS.Create(traceLog)
	if err != nil {
		panic(err)
	}

	traceFile = file
	err = trace.Start(traceFile)
	if err != nil {
		panic(err)
	}
}

func tearDownTraceLog(cmd *cobra.Command) {
	flags := cmd.Flags()
	traceLog, _ := flags.GetString("trace-log")
	if traceLog == "" {
		return
	}
	trace.Stop()
	err := traceFile.Close()
	if err != nil {
		panic(err)
	}
}

func resourceLimits(cmd *cobra.Command) {
	flags := cmd.Flags()
	cores, err := flags.GetFloat64("max-cpus")
	if err == nil {
		eventbus.Publish(eventbus.EvSysLimitCPU, cores)
	}

	fds, err := flags.GetFloat64("max-files")
	if err == nil {
		eventbus.Publish(eventbus.EvSysLimitFile, fds)
	}
}

func parseV1Path(conf define.Configuration) define.Configuration {
	pathVersion := "v1"
	var clusterID string

	// 将consul.path_version 设置为 v1
	conf.Set(consul.ConfKeyPathVersion, pathVersion)
	if clusterName != "" {
		clusterID = clusterName
		conf.Set(consul.ConfKeyClusterID, clusterName)
	} else {
		clusterID = conf.GetString(consul.ConfKeyClusterID)
	}

	if host != "" {
		conf.Set(define.ConfHost, host)
	}

	if port != "" {
		conf.Set(define.ConfPort, port)
	}

	servicePath := conf.GetString(consul.ConfKeyServicePath)
	define.ConfRootV1 = path.Join(servicePath, pathVersion)
	define.ConfClusterID = clusterID

	conf.Set(consul.ConfKeyServicePath, path.Join(servicePath, pathVersion, clusterID))
	conf.Set(consul.ConfKeyManualPath, path.Join(conf.GetString(consul.ConfKeyManualPath), pathVersion, clusterID))
	conf.Set(consul.ConfKeyDataIDPath, path.Join(conf.GetString(consul.ConfKeyDataIDPath), pathVersion, clusterID, "data_id"))

	return conf
}

func parseV0Path(conf define.Configuration) define.Configuration {
	return conf
}
