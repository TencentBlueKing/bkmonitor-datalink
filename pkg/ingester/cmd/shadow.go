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
	"strconv"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/consul"
)

// shadowCmd represents the version command
var shadowCmd = &cobra.Command{
	Use:   "shadow",
	Short: "Print shadow info",
	Run: func(cmd *cobra.Command, args []string) {
		cobra.CheckErr(config.Init())

		plan, err := consul.ListDispatchPlan()
		cobra.CheckErr(err)

		fmt.Printf("DataID path: %s\n", config.Configuration.Consul.GetDataIDPathPrefix())
		fmt.Printf("Shadow path: %s\n", config.Configuration.Consul.GetShadowPathPrefix())

		table := tablewriter.NewWriter(os.Stdout)

		tableHeader := []string{"service", "data_id", "plugin_id", "type", "target"}
		table.SetHeader(tableHeader)

		serviceCount := 0
		linkCount := 0
		for service, pairs := range plan {
			for _, pair := range pairs {
				plugin := pair.DataSource.MustGetPluginOption()
				tableRow := []string{
					service, strconv.Itoa(pair.DataSource.DataID),
					plugin.PluginID, plugin.PluginType, pair.Pair.Key,
				}
				table.Append(tableRow)
				linkCount += 1
			}
			serviceCount += 1
		}
		table.SetCaption(true, fmt.Sprintf("%d services, %d links\n", serviceCount, linkCount))
		table.Render()
	},
}

func init() {
	rootCmd.AddCommand(shadowCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// clusterCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// clusterCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
