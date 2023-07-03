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

	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

// metaCmd represents the meta command
var metaCmd = &cobra.Command{
	Use:   "meta",
	Short: "Print meta info",
	Run: func(cmd *cobra.Command, args []string) {
		define.VisitPlugins(func(info *define.PluginInfo) {
			registered := info.Registered()
			num := len(registered)
			if num == 0 {
				fmt.Printf("nothing registered %s: \n", info.Name)
				return
			}

			fmt.Printf("%d registered %s: ", num, info.Name)
			for i, value := range registered {
				if value == "" {
					value = "''"
				}
				if i == num-1 {
					fmt.Printf("%s\n", value)
				} else {
					fmt.Printf("%s, ", value)
				}
			}
		})
	},
}

func init() {
	rootCmd.AddCommand(metaCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// metaCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// metaCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
