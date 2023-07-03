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
	"strings"

	"github.com/cstockton/go-conv"
	"github.com/dghubble/sling"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/http"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// signalCmd represents the signal command
var signalCmd = &cobra.Command{
	Use:     "signal",
	Short:   "Send signal",
	Long:    `Send signal to daemon service`,
	Example: `transfer signal -i ${service} -s set-log-level -v level:debug`,
	Run: func(cmd *cobra.Command, args []string) {
		flags := cmd.Flags()

		conf := config.Configuration
		var host string
		var port int

		helper, err := scheduler.NewClusterHelper(context.Background(), conf)
		checkError(err, -1, "cluster config failed")

		name, err := flags.GetString("service")
		checkError(err, -2, "get service name failed")
		if name == "" {
			exitf(1, "get service name failed")
		}

		signal, err := flags.GetString("signal")
		checkError(err, -2, "get signal failed: %v", err)
		if signal == "" {
			exitf(1, "available signals: %s", strings.Join(define.ListSignalNames(), ", "))
		}
		_, matched := define.GetSignalByName(signal)
		if !matched {
			exitf(1, "signal %s is not available", signal)
		}

		vars, err := flags.GetStringArray("vars")
		checkError(err, -2, "get vars failed: %v", err)

		clusterInfo, err := helper.ListServices()
		checkError(err, -3, "list cluster services failed")

		info, ok := clusterInfo[name]
		if !ok {
			exitf(-4, "list cluster services failed")
		}

		host = info.Address
		port = info.Port

		table := tablewriter.NewWriter(os.Stdout)
		table.SetCaption(true, fmt.Sprintf("signal %s to service %s", signal, name))
		table.SetAutoMergeCells(true)
		table.SetRowLine(true)
		table.SetHeader([]string{"group", "name", "content"})

		params := make(map[string]string)
		for _, v := range vars {
			parts := strings.Split(v, ":")
			if len(parts) != 2 {
				exitf(1, "unrecognized vars %v", v)
			}

			key := parts[0]
			value := parts[1]
			params[key] = value
			table.Append([]string{"vars", key, value})
		}
		if v, err := flags.GetInt("max_worker"); v != 0 {
			utils.CheckError(err)
			params["max_worker"] = conv.String(v)
		}
		user, password := http.GetBasicAuthInfo(conf)
		response, err := sling.New().
			Base(fmt.Sprintf("http://%s:%d/signal/", host, port)).
			SetBasicAuth(user, password).
			Post(signal).
			BodyJSON(params).
			ReceiveSuccess(nil)
		checkError(err, -4, "send signal failed")

		for key, value := range response.Header {
			if strings.HasPrefix(key, "X-") {
				table.Append([]string{"response", key[2:], strings.Join(value, ",")})
			}
		}

		table.Append([]string{"meta", "address", fmt.Sprintf("%s:%d", host, port)})
		table.Append([]string{"meta", "status", fmt.Sprintf("%s:%d", response.Status, response.StatusCode)})

		table.Render()
	},
}

func init() {
	rootCmd.AddCommand(signalCmd)
	flags := signalCmd.Flags()
	flags.StringP("service", "i", "", "id of service")
	flags.StringP("signal", "s", "", "name of signal")
	flags.StringArrayP("vars", "v", []string{}, "request var")
	flags.IntP("max_worker", "w", config.Configuration.GetInt("max_worker"), "update max worker number")

	utils.CheckError(signalCmd.MarkFlagRequired("service"))
	utils.CheckError(signalCmd.MarkFlagRequired("signal"))

	define.RegisterSignalName(`update-cc-cache`, eventbus.EvSigUpdateCCCache)
	define.RegisterSignalName(`dump-host-info`, eventbus.EvSigDumpHostInfo)
	define.RegisterSignalName(`dump-instance-info`, eventbus.EvSigDumpInstanceInfo)
	define.RegisterSignalName(`set-log-level`, eventbus.EvSigSetLogLevel)
	define.RegisterSignalName(`dump-stack`, eventbus.EvSigDumpStack)
	define.RegisterSignalName(`set-block-profile`, eventbus.EvSigSetBlockProfile)
	define.RegisterSignalName(`limit-resource`, eventbus.EvSigLimitResource)
	define.RegisterSignalName(`commit-cache`, eventbus.EvSigCommitCache)
	define.RegisterSignalName(`update-cc-worker`, eventbus.EvSigUpdateCCWorker)
	define.RegisterSignalName(`update-mem-cache`, eventbus.EvSigUpdateMemCache)
}
