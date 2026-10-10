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
	"os"

	goRedis "github.com/go-redis/redis/v8"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	yaml "gopkg.in/yaml.v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	redisService "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/redis"
)

// configCmd represents the config command
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "show current config",
	Run: func(cmd *cobra.Command, args []string) {
		config.InitConfig()

		output, err := yaml.Marshal(viper.AllSettings())
		if err != nil {
			fmt.Printf("failed to marshal config for->[%s]", err)
			return
		}
		fmt.Printf("%s", output)
	},
}

func newSetFeatureFlagsCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:          "set-feature-flags",
		Short:        "set the complete feature flag JSON snapshot in Redis",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile(file)
			if err != nil {
				return fmt.Errorf("read feature flag file: %w", err)
			}
			if err := featureFlag.ValidateFeatureFlagSnapshot(data); err != nil {
				return err
			}

			if err := loadFeatureFlagConfig(cmd); err != nil {
				return err
			}
			client := goRedis.NewUniversalClient(redisService.ClientOptions())
			defer client.Close()
			flags := redis.NewFeatureFlagClient(client, redisService.KVBasePath)
			if err := flags.SetFeatureFlags(cmd.Context(), data); err != nil {
				return err
			}
			cmd.Printf("feature flags saved to Redis key %s\n", flags.GetFeatureFlagsPath())
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "complete feature flag JSON snapshot file")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newResetFeatureFlagsCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "reset-feature-flags",
		Short:        "delete the Redis feature flag snapshot and notify running UQ to migrate from Consul again",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := loadFeatureFlagConfig(cmd); err != nil {
				return err
			}
			client := goRedis.NewUniversalClient(redisService.ClientOptions())
			defer client.Close()
			flags := redis.NewFeatureFlagClient(client, redisService.KVBasePath)
			if err := flags.ResetFeatureFlags(cmd.Context()); err != nil {
				return err
			}
			cmd.Printf("Redis feature flag snapshot deleted: %s; running UQ will retry migration from Consul\n", flags.GetFeatureFlagsPath())
			return nil
		},
	}
}

func loadFeatureFlagConfig(cmd *cobra.Command) error {
	log.SetOutput(cmd.ErrOrStderr())
	if err := config.InitConfigWithWriter(cmd.ErrOrStderr()); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if redisService.KVBasePath == "" {
		return fmt.Errorf("redis.kv_base_path must not be empty")
	}
	return nil
}

func init() {
	configCmd.AddCommand(newSetFeatureFlagsCmd(), newResetFeatureFlagsCmd())
	configCmd.AddCommand(newGetFeatureFlagsCmd(), newMutateFeatureFlagCmd("add"), newMutateFeatureFlagCmd("update"), newMutateFeatureFlagCmd("delete"))
	rootCmd.AddCommand(configCmd)
}
