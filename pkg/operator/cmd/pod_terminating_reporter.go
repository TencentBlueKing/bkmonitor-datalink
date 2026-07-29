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
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/podterminatingreporter"
)

type podTerminatingReporterRunFunc func(context.Context, podterminatingreporter.ReporterOptions) error

func newPodTerminatingReporterCommand(run podTerminatingReporterRunFunc) *cobra.Command {
	options := podterminatingreporter.ReporterOptions{}
	command := &cobra.Command{
		Use:   "pod-terminating-reporter",
		Short: "Expose Pod terminating durations as continuous time series",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			namespace, err := podterminatingreporter.ResolveNamespace(
				options.Namespace,
				podterminatingreporter.ServiceAccountNamespacePath,
			)
			if err != nil {
				return err
			}
			options.Namespace = namespace
			if err := options.Validate(); err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(command.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return run(ctx, options)
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.ListenAddress, "listen-address", ":9110", "HTTP listen address")
	flags.StringVar(&options.Namespace, "namespace", "", "Namespace containing the state ConfigMap; defaults to the ServiceAccount namespace")
	flags.StringVar(&options.StateConfigMapName, "state-configmap-name", "pod-terminating-reporter-state", "State ConfigMap name")
	flags.DurationVar(&options.RefreshInterval, "refresh-interval", time.Minute, "Complete refresh interval")
	flags.Int64Var(&options.PageLimit, "page-limit", 200, "Kubernetes Pod list page size")
	flags.DurationVar(&options.RequestTimeout, "request-timeout", 15*time.Second, "Timeout applied independently to each Kubernetes API request")
	flags.DurationVar(&options.RecoveryHold, "recovery-hold", 10*time.Minute, "Duration to expose zero-valued recovery tombstones")
	flags.DurationVar(&options.StaleAfter, "stale-after", 3*time.Minute, "Maximum age of business rows after the latest successful refresh")
	flags.IntVar(&options.StateMaxBytes, "state-max-bytes", podterminatingreporter.HardMaxStateBytes, "Maximum compact state.json bytes (cannot exceed 900000)")
	return command
}

func init() {
	rootCmd.AddCommand(newPodTerminatingReporterCommand(podterminatingreporter.RunInCluster))
}
