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
	"sort"
	"strconv"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

// flowCmd represents the flow command
var flowCmd = &cobra.Command{
	Use:   "flow",
	Short: "Print the flow detailed",
	Run: func(cmd *cobra.Command, args []string) {
		flags := cmd.Flags()

		groupby, err := flags.GetStringSlice("groupby")
		checkError(err, -1, "failed to parse groupby filed slice")
		if len(groupby) >= 1 && !validateType(groupby[0]) {
			fmt.Fprint(os.Stderr, "'groupby' must provide at least one legal type, optional(dataid,service,type)\n")
			os.Exit(1)
		}

		sumby, err := flags.GetStringSlice("sumby")
		checkError(err, -1, "failed to parse sumby filed slice")
		if len(sumby) >= 1 && !validateType(sumby[0]) {
			fmt.Fprint(os.Stderr, "'sumby' must provide at least one legal type, optional(dataid,service,type)\n")
			os.Exit(1)
		}

		table := tablewriter.NewWriter(os.Stdout)

		detailed, err := consul.SchedulerHelper.List()
		defer consul.SchedulerHelper.Close()

		checkError(err, -1, "consul error, failed to get dataid flow detailed")

		header := []string{"path", "cluster", "service", "dataid", "type", "flow"}
		if len(groupby) >= 1 {
			table.SetHeader(header)

			items := detailed.GroupBy(groupby[0], groupby[1:]...)
			for _, v := range items {
				for _, item := range v {
					table.Append([]string{
						item.Path,
						item.Cluster,
						item.Service,
						strconv.Itoa(item.DataID),
						item.Type,
						strconv.Itoa(item.Flow),
					})
				}
			}
			table.Render()
			return
		}

		if len(sumby) >= 1 {
			items := detailed.SumBy(sumby[0])
			type T struct {
				k string
				v int
			}

			ls := make([]T, 0)
			for k, v := range items {
				ls = append(ls, T{k: k, v: v})
			}
			sort.Slice(ls, func(i, j int) bool {
				return ls[i].v > ls[j].v
			})

			percent := detailed.SumPercentBy(sumby[0])
			table.SetHeader([]string{sumby[0], "flow(Bytes)", "percent(%)"})

			for _, l := range ls {
				table.Append([]string{l.k, strconv.Itoa(l.v), fmt.Sprintf("%f", percent[l.k])})
			}

			table.Render()
			return
		}

		table.SetHeader(header)
		for _, item := range detailed {
			table.Append([]string{
				item.Path,
				item.Cluster,
				item.Service,
				strconv.Itoa(item.DataID),
				item.Type,
				strconv.Itoa(item.Flow),
			})
		}
		table.Render()
	},
}

func validateType(t string) bool {
	for _, typ := range [...]string{define.FlowItemKeyDataID, define.FlowItemKeyService, define.FlowItemKeyType} {
		if typ == t {
			return true
		}
	}

	return false
}

func init() {
	rootCmd.AddCommand(flowCmd)
	flags := flowCmd.Flags()
	flags.StringSliceP("groupby", "", []string{}, "group by key, split by commas. for example: dataid,1001,1002")
	flags.StringSliceP("sumby", "", []string{}, "sum by key, optional:(dataid,service,type)")
}
