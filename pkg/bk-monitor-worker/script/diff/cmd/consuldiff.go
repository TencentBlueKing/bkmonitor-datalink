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

	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/script/diff/consul"
)



func init() {
	rootCmd.AddCommand(consulDiffCmd)
	consulDiffCmd.PersistentFlags().StringVar(
		&consul.ConsulDiffConfigPath, "config", "", "config file path",
	)

	consulDiffCmd.PersistentFlags().StringVar(
		&consul.Config.Src.Address, "srcAddress", "127.0.0.1:8500", "consul address",
	)
	consulDiffCmd.PersistentFlags().IntVar(
		&consul.Config.Src.Port, "port", 8500, "consul port",
	)
	consulDiffCmd.PersistentFlags().StringVar(
		&consul.Config.Src.Path, "srcPath", "", "consul src path",
	)

	consulDiffCmd.PersistentFlags().StringVar(
		&consul.Config.Bypass.Address, "bypassAddress", "127.0.0.1:8500", "consul address",
	)
	consulDiffCmd.PersistentFlags().IntVar(
		&consul.Config.Bypass.Port, "bypassPort", 8500, "consul port",
	)
	consulDiffCmd.PersistentFlags().StringVar(
		&consul.Config.Bypass.Path, "bypassPath", "", "consul src path",
	)
}

var consulDiffCmd = &cobra.Command{
	Use:   "consul_diff",
	Short: "diff for consul",
	Long:  "diff content from consul src and dst path",
	Run:   func(cmd *cobra.Command, args []string){
		// 将命令行参数绑定到 Viper
		if err := consul.InitConfig(); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		settings := fmt.Sprintf(`output different consul content 
src address: %s, port: %d src path: %s 
bypass address: %s, port: %d src path: %s
`, consul.Config.Src.Address, consul.Config.Src.Port, consul.Config.Src.Path, consul.Config.Bypass.Address, consul.Config.Bypass.Port, consul.Config.Bypass.Path)
		fmt.Println(settings)

		// 输出原路径和旁路路径的差异
		consul.OutputDiffContent()
	},
}
