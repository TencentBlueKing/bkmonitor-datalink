// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	goRedis "github.com/go-redis/redis/v8"
	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	redisService "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/redis"
)

func newGetFeatureFlagsCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:          "get-feature-flags",
		Short:        "read the Redis snapshot or a single feature flag as JSON",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := loadFeatureFlagConfig(cmd); err != nil {
				return err
			}
			client := goRedis.NewUniversalClient(redisService.ClientOptions())
			defer client.Close()
			flags := redis.NewFeatureFlagClient(client, redisService.KVBasePath)
			data, err := flags.GetFeatureFlags(cmd.Context())
			if err != nil {
				return err
			}
			if data == nil {
				return fmt.Errorf("Redis feature flag snapshot does not exist")
			}
			if name != "" {
				var snapshot map[string]json.RawMessage
				if err := json.Unmarshal(data, &snapshot); err != nil {
					return fmt.Errorf("decode feature flag snapshot: %w", err)
				}
				var ok bool
				data, ok = snapshot[name]
				if !ok {
					return fmt.Errorf("feature flag %q does not exist", name)
				}
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
			return err
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "read only this flag; omit to read the complete snapshot")
	return cmd
}

func newMutateFeatureFlagCmd(action string) *cobra.Command {
	var name, file string
	cmd := &cobra.Command{
		Use:          action + "-feature-flag",
		Short:        action + " a single feature flag without replacing other flags",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("feature flag name must not be empty")
			}
			var data []byte
			if action != "delete" {
				var err error
				data, err = os.ReadFile(file)
				if err != nil {
					return fmt.Errorf("read feature flag file: %w", err)
				}
				snapshot, err := json.Marshal(map[string]json.RawMessage{name: data})
				if err != nil {
					return fmt.Errorf("invalid feature flag JSON: %w", err)
				}
				if err := featureFlag.ValidateFeatureFlagSnapshot(snapshot); err != nil {
					return err
				}
			}
			if err := loadFeatureFlagConfig(cmd); err != nil {
				return err
			}
			client := goRedis.NewUniversalClient(redisService.ClientOptions())
			defer client.Close()
			flags := redis.NewFeatureFlagClient(client, redisService.KVBasePath)
			var err error
			switch action {
			case "add":
				err = flags.AddFeatureFlag(cmd.Context(), name, data)
			case "update":
				err = flags.UpdateFeatureFlag(cmd.Context(), name, data)
			case "delete":
				err = flags.DeleteFeatureFlag(cmd.Context(), name)
			default:
				return fmt.Errorf("unknown feature flag operation %q", action)
			}
			if err != nil {
				return err
			}
			cmd.Printf("feature flag %q: %s completed in Redis\n", name, action)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "feature flag name")
	_ = cmd.MarkFlagRequired("name")
	if action != "delete" {
		cmd.Flags().StringVar(&file, "file", "", "JSON definition of this flag without the outer name")
		_ = cmd.MarkFlagRequired("file")
	}
	return cmd
}
