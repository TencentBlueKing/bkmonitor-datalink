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

type podTerminatingStateInitRunFunc func(context.Context, podterminatingreporter.StateInitOptions) error

func newPodTerminatingStateInitCommand(run podTerminatingStateInitRunFunc) *cobra.Command {
	options := podterminatingreporter.StateInitOptions{}
	command := &cobra.Command{
		Use:   "pod-terminating-state-init",
		Short: "Create or strictly validate the Pod terminating reporter state",
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
	flags.StringVar(&options.Namespace, "namespace", "", "Namespace containing the state ConfigMap; defaults to the ServiceAccount namespace")
	flags.StringVar(&options.StateConfigMapName, "state-configmap-name", "pod-terminating-reporter-state", "State ConfigMap name")
	flags.DurationVar(&options.RequestTimeout, "request-timeout", 15*time.Second, "Timeout applied to each Kubernetes API request")
	flags.IntVar(&options.StateMaxBytes, "state-max-bytes", podterminatingreporter.HardMaxStateBytes, "Maximum compact state.json bytes (cannot exceed 900000)")
	return command
}

func init() {
	rootCmd.AddCommand(newPodTerminatingStateInitCommand(podterminatingreporter.RunStateInitInCluster))
}
