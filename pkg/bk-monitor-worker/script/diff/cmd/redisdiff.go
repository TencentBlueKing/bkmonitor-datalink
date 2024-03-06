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

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/script/diff/redis"
)

var redisCmd = &cobra.Command{
	Use:   "redis_diff",
	Short: "diff redis data",
	Long:  "diff redis data",
	Run:   startRedisDiff,
}

func init() {
	redisCmd.PersistentFlags().StringVar(&redis.ConfigPath, "config", "./diff.yaml", "path of diff config files")
	redisCmd.Flags().String("srcKey", "", "src redis key")
	redisCmd.Flags().String("bypassKey", "", "bypass redis key")
	redisCmd.Flags().String("keyType", "", "key type [string, hash, list, set]")
	rootCmd.AddCommand(redisCmd)
}

func startRedisDiff(cmd *cobra.Command, args []string) {
	redis.InitConfig()
	redis.InitRedisDiffConfig()

	srcKey, _ := cmd.Flags().GetString("srcKey")
	if srcKey != "" {
		// 命令行参数覆盖配置文件参数
		viper.Set("diffRedis.srcKey", srcKey)
	} else {
		srcKey = viper.GetString("diffRedis.srcKey")
		if srcKey == "" {
			fmt.Println("srcKey can not be empty")
			os.Exit(1)
		}
	}

	bypassKey, _ := cmd.Flags().GetString("bypassKey")
	if bypassKey != "" {
		// 命令行参数覆盖配置文件参数
		viper.SetDefault("diffRedis.bypassKey", bypassKey)
	} else {
		bypassKey = viper.GetString("diffRedis.bypassKey")
		if bypassKey == "" {
			fmt.Println("bypassKey can not be empty")
			os.Exit(1)
		}
	}

	if keyType, _ := cmd.Flags().GetString("keyType"); keyType != "" {
		// 命令行参数覆盖配置文件参数
		viper.Set("diffRedis.keyType", keyType)
	} else {
		// 命令行参数未配置则使用配置文件参数，默认为hash
		viper.SetDefault("diffRedis.keyType", "hash")
	}

	redis.DiffConfig.KeyType = viper.GetString("diffRedis.keyType")
	redis.DiffConfig.SrcKey = srcKey
	redis.DiffConfig.BypassKey = bypassKey
	du := redis.DiffUtil{
		Config: redis.DiffConfig,
	}
	equal, err := du.Diff()
	if err != nil {
		fmt.Printf("diff key [%s] and [%s] %s data failed, %v\n", du.SrcKey, du.BypassKey, du.KeyType, err)
		os.Exit(1)
	}
	if equal {
		fmt.Printf("key [%s] and [%s] %s data is equal\n", du.SrcKey, du.BypassKey, du.KeyType)
	} else {
		fmt.Printf("key [%s] and [%s] %s data is different\n", du.SrcKey, du.BypassKey, du.KeyType)
	}

}
