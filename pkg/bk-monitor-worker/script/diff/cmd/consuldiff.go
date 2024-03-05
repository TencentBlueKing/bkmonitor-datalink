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
		&consul.Address, "address", "127.0.0.1:8500", "consul address",
	)
	consulDiffCmd.PersistentFlags().IntVar(
		&consul.Port, "port", 8500, "consul port",
	)
	consulDiffCmd.PersistentFlags().StringVar(
		&consul.Addr, "addr", "http://127.0.0.1:8500", "consul address with schema",
	)
	consulDiffCmd.PersistentFlags().StringVar(
		&consul.SrcPath, "src_path", "", "consul src path",
	)
	consulDiffCmd.PersistentFlags().StringVar(
		&consul.DstPath, "dst_path", "", "consul dst path",
	)
	consulDiffCmd.PersistentFlags().StringVar(
		&consul.BypassName, "bypass_name", "_bypass", "consul bypass name",
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
		fmt.Printf("output different consul content, settings: \naddress: %s, port: %d \nsrc path: %s \ndst path: %s\n\n", consul.Address, consul.Port, consul.SrcPath, consul.DstPath)
		// 输出原路径和旁路路径的差异
		consul.OutputDiffContent()
	},
}
