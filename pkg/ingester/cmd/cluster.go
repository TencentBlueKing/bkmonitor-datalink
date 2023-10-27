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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/utils"
)

// clusterCmd represents the version command
var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Print cluster info",
	Run: func(cmd *cobra.Command, args []string) {
		cobra.CheckErr(config.Init())
		serviceEntries, err := consul.ListServices(true)
		cobra.CheckErr(err)
		leaderService, err := consul.GetLeader()
		cobra.CheckErr(err)
		table := tablewriter.NewWriter(os.Stdout)
		table.SetCaption(true, fmt.Sprintf("%d services found\n", len(serviceEntries)))

		tableHeader := []string{"service", "role", "status", "address", "port", "tags", "meta"}
		table.SetHeader(tableHeader)

		for _, serviceEntry := range serviceEntries {
			role := ""
			if serviceEntry.Service.ID == leaderService.ID {
				role = "leader"
			}
			tableRow := []string{
				serviceEntry.Service.ID, role, serviceEntry.Checks.AggregatedStatus(),
				serviceEntry.Service.Address, strconv.Itoa(serviceEntry.Service.Port),
				utils.ReadableStringList(serviceEntry.Service.Tags), utils.ReadableStringMap(serviceEntry.Service.Meta),
			}
			table.Append(tableRow)
		}

		table.Render()
	},
}

func init() {
	rootCmd.AddCommand(clusterCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// clusterCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// clusterCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
