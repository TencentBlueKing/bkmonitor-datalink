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
	"gopkg.in/yaml.v2"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
)

// confinfoCmd represents the confinfo command
var confinfoCmd = &cobra.Command{
	Use:   "confinfo",
	Short: "Show configure info",
	Run: func(cmd *cobra.Command, args []string) {
		out, err := yaml.Marshal(config.Configuration.AllSettings())
		if err != nil {
			panic(err)
		}

		fmt.Println(string(out))
	},
}

func init() {
	rootCmd.AddCommand(confinfoCmd)
}
