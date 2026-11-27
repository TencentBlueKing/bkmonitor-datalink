// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package cmd

import (
	"errors"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/log"
)

func init() {
	rootCmd.AddCommand(newMetricMetadataCmd())
}

func newMetricMetadataCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start_metricmetadata",
		Short: "start the metric metadata Kafka consumer independently",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			config.InitConfig()
			log.InitLogger()

			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
			defer stop()

			consumerErrors := make(chan error, 1)
			go func() {
				consumerErrors <- metricmetadata.RunStandalone(ctx)
			}()

			select {
			case err := <-consumerErrors:
				if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
					return nil
				}
				return err
			case <-ctx.Done():
				err := <-consumerErrors
				if errors.Is(err, ctx.Err()) {
					return nil
				}
				return err
			}
		},
	}
}
