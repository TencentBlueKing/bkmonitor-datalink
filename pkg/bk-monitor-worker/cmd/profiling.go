// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmd

import (
	"context"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/log"
)

func init() {
	rootCmd.AddCommand(startProfilingCmd)
	startProfilingCmd.Flags().StringVarP(&profilingConfigFile, "file", "f", "", "optional Active Profiling task config file; defaults to BMW Consul DataIDs")
}

var profilingConfigFile string

var startProfilingCmd = &cobra.Command{
	Use:   "start_profiling",
	Short: "start Active Profiling tasks from a custom config file or BMW Consul",
	RunE:  startProfiling,
}

func startProfiling(_ *cobra.Command, _ []string) error {
	config.InitConfig()
	log.InitLogger()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	processor, err := profiling.Initial(ctx)
	if err != nil {
		return err
	}
	return processor.RunStandalone(profilingConfigFile)
}
