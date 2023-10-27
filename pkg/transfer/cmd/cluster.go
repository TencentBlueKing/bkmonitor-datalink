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

	"github.com/cstockton/go-conv"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// clusterCmd represents the cluster command
var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Print cluster info",
	Run: func(cmd *cobra.Command, args []string) {
		pathVersion := config.Configuration.GetString(consul.ConfKeyPathVersion)

		helper, err := scheduler.NewClusterHelper(context.Background(), config.Configuration)
		checkError(err, -1, "cluster config failed")

		// 根据不同的路径规则初始化不同的获取方法
		listLeaders, listServices := parseFnsByPathVersion(pathVersion, helper)

		clusterInfo, err := listServices()
		logging.WarnIf("get cluster service information failed", err)

		leaderInfo, err := listLeaders()
		logging.WarnIf("get cluster leader information failed", err)

		table := tablewriter.NewWriter(os.Stdout)
		table.SetCaption(true, fmt.Sprintf("%d healthy services found\n", len(clusterInfo)))

		tableHeader := []string{"cluster", "", "service", "address", "port", "tags", "meta"}
		table.SetHeader(parseTableByPathVersion(pathVersion, tableHeader))

		for id, info := range clusterInfo {
			flags := make([]string, 0)

			if _, ok := leaderInfo[id]; ok {
				flags = append(flags, "leader")
			}

			clusterID := info.Meta["cluster_id"]
			tableRow := parseTableByPathVersion(pathVersion, []string{
				clusterID, utils.ReadableStringList(flags), info.ID, info.Address, conv.String(info.Port),
				utils.ReadableStringList(info.Tags), utils.ReadableStringMap(info.Meta),
			})
			table.Append(tableRow)
		}

		table.Render()
	},
}

// 根据不同的路径规则，初始化不同的展示leader和services的方法
func parseFnsByPathVersion(pathVersion string, helper *scheduler.ClusterHelper) (
	listLeaders, listServices func() (map[string]*define.ServiceInfo, error),
) {
	switch pathVersion {
	case "":
		return helper.ListLeaders, helper.ListServices
	default:
		return helper.ListAllLeaders, helper.ListAllServices
	}
}

// 根据不同的路径规则，处理打印出的表头，和表行
func parseTableByPathVersion(pathVersion string, tableRow []string) []string {
	switch pathVersion {
	case "":
		return tableRow[1:]
	default:
		return tableRow
	}
}

func init() {
	rootCmd.AddCommand(clusterCmd)
}
