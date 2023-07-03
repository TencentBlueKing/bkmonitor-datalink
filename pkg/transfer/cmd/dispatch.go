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

	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/scheduler"
)

// dispatchCmd represents the dispatch command
var dispatchCmd = &cobra.Command{
	Use:   "dispatch",
	Short: "Dispatch shadows",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		helper, err := scheduler.NewClusterHelper(ctx, config.Configuration)
		checkError(err, -1, "cluster config failed")

		services, err := helper.Service.Info(define.ServiceTypeAll)
		checkError(err, -2, "list services failed")

		api := helper.Client.KV()
		pairs, _, _ := api.List(helper.DataIDRoot, nil)

		dispatcher := helper.Dispatcher
		checkError(dispatcher.Recover(), -3, "recover shadows failed")

		fmt.Printf("dispatch %d pairs to %d services\n", len(pairs), len(services))
		checkError(dispatcher.Dispatch(pairs, services), -4, "dispatch failed")
	},
}

func init() {
	rootCmd.AddCommand(dispatchCmd)
}
