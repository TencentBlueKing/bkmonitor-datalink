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
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/transport"
	"github.com/spf13/cobra"
)

// transportCmd represents the transport command
var transportCmd = &cobra.Command{
	Use:   "transport",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("transport called")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		address := common.Config.GetString(common.ConfigKeyConsulAddress)
		prefix := common.Config.GetString(common.ConfigKeyConsulPrefix)
		caCertFile := common.Config.GetString(common.ConfigKeyConsulCACertFile)
		certFile := common.Config.GetString(common.ConfigKeyConsulCertFile)
		keyFile := common.Config.GetString(common.ConfigKeyConsulKeyFile)
		skipVerify := common.Config.GetBool(common.ConfigKeyConsulSkipVerify)

		periodParam, err := cmd.Flags().GetString("period")
		if err != nil {
			logging.StdLogger.Errorf("get period failed,error:%s", err)
			return
		}
		durationParam, err := cmd.Flags().GetString("duration")
		if err != nil {
			logging.StdLogger.Errorf("get duration failed,error:%s", err)
			return
		}
		maxLines, err := cmd.Flags().GetInt("maxlines")
		if err != nil {
			logging.StdLogger.Errorf("get maxlines failed,error:%s", err)
			return
		}
		batchSize, err := cmd.Flags().GetInt("batchsize")
		if err != nil {
			logging.StdLogger.Errorf("get batchsize failed,error:%s", err)
			return
		}

		period, err := time.ParseDuration(periodParam)
		if err != nil {
			logging.StdLogger.Errorf("parse period failed,error:%s", err)
			return
		}
		tlsConfig := &config.TlsConfig{
			CAFile:     caCertFile,
			CertFile:   certFile,
			KeyFile:    keyFile,
			SkipVerify: skipVerify,
		}
		err = consul.Init(address, prefix, tlsConfig)
		if err != nil {
			logging.StdLogger.Errorf("consul init failed,error:%s", err)
			return
		}
		trans := transport.NewTransport(ctx, durationParam, maxLines, batchSize)
		sessionID, err := consul.NewSession(ctx)
		logging.StdLogger.Infof("transport get new session id:%s", sessionID)

		ticker := time.NewTicker(period)
		logging.StdLogger.Infof("transport start period check task,period:%s", period)
		for {
			select {
			case <-ticker.C:
				err = trans.CheckTagInfos(sessionID)
				if err != nil {
					logging.StdLogger.Errorf("check tag infos failed,error:%s", err)
					break
				}
			case <-ctx.Done():
				logging.StdLogger.Info("transport get ctx done,exit")
				return
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(transportCmd)
	transportCmd.Flags().StringP("period", "p", "30s", "refresh period")
	transportCmd.Flags().StringP("duration", "d", "2h", "query duration in each query")
	transportCmd.Flags().IntP("maxlines", "m", 5000, "max lines in each query")
	transportCmd.Flags().IntP("batchsize", "b", 5000, "max lines in each write")

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// transportCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// transportCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
