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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/config"
)

// versionCmd represents the version command
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "show version ",
	Long:  `show version and exit`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("%s", config.Version)
		if cmd.Flag("detail").Changed {
			fmt.Printf("-%s", config.CommitHash)
		}
		fmt.Printf("\n")
	},
}

// init 加载默认配置
func init() {
	rootCmd.AddCommand(versionCmd)
	versionCmd.Flags().BoolP("detail", "d", false, "show detail version info")
}
