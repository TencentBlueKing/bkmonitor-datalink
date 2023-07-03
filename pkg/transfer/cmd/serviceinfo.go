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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// versionCmd represents the serviceinfo command
var serviceCmd = &cobra.Command{
	Use:   "serviceinfo",
	Short: "Print service info",
	Run: func(cmd *cobra.Command, args []string) {
		ServiceID := utils.GetServiceID(config.Configuration)
		name := config.Configuration.GetString(consul.ConfKeyServiceName)
		fmt.Println("service:", fmt.Sprintf("%s-%s", name, ServiceID))
		fmt.Println("service_name:", fmt.Sprintf("%s-%s", define.AppName, ServiceID))
		fmt.Println("ID:", ServiceID)
	},
}

func init() {
	rootCmd.AddCommand(serviceCmd)
}
