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
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

// versionCmd represents the version command
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version info",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("%s\n", define.Version)
		all, _ := cmd.Flags().GetBool("all")
		if all {
			fmt.Printf("%s\n", define.BuildHash)
		}
	},
}

func init() {
	if define.Version == "" {
		panic(errors.New("version is empty"))
	}

	if define.BuildHash == "" {
		panic(errors.New("build hash is empty"))
	}

	rootCmd.AddCommand(versionCmd)
	versionCmd.Flags().BoolP("all", "a", false, "show all version info")
}
