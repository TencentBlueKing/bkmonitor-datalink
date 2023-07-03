// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package instance

import (
	"time"

	"github.com/spf13/viper"
)

const (
	ConfigPathCosRegion    = "cos.region"
	ConfigPathCosUrl       = "cos.url"
	ConfigPathCosBucket    = "cos.bucket"
	ConfigPathCosAppID     = "cos.app_id"
	ConfigPathCosSecretID  = "cos.secret_id"
	ConfigPathCosSecretKey = "cos.secret_key"

	ConfigPathCosPartSize       = "cos.part_size"
	ConfigPathMaxRetries        = "cos.max_retries"
	ConfigPathCosThreadPoolSize = "cos.thread_pool_size"
	ConfigPathCosTimeout        = "cos.timeout"
	ConfigPathCosTempDir        = "cos.temp_dir"
)

var (
	CosRegion    string
	CosUrl       string
	CosBucket    string
	CosAppID     string
	CosSecretID  string
	CosSecretKey string

	CosPartSize       int64
	CosMaxRetries     int
	CosThreadPoolSize int
	CosTimeout        time.Duration

	CosTempDir string
)

func ConfigInit() error {

	viper.SetDefault(ConfigPathCosThreadPoolSize, 10)
	viper.SetDefault(ConfigPathCosTimeout, "10m")
	viper.SetDefault(ConfigPathCosPartSize, 2*1024*1024)
	viper.SetDefault(ConfigPathMaxRetries, 3)
	viper.SetDefault(ConfigPathCosTempDir, "download_temp")

	CosRegion = viper.GetString(ConfigPathCosRegion)
	CosUrl = viper.GetString(ConfigPathCosUrl)
	CosBucket = viper.GetString(ConfigPathCosBucket)
	CosAppID = viper.GetString(ConfigPathCosAppID)
	CosSecretID = viper.GetString(ConfigPathCosSecretID)
	CosSecretKey = viper.GetString(ConfigPathCosSecretKey)

	CosPartSize = viper.GetInt64(ConfigPathCosPartSize)
	CosMaxRetries = viper.GetInt(ConfigPathMaxRetries)
	CosThreadPoolSize = viper.GetInt(ConfigPathCosThreadPoolSize)
	CosTimeout = viper.GetDuration(ConfigPathCosTimeout)
	CosTempDir = viper.GetString(ConfigPathCosTempDir)

	return nil
}
