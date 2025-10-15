// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

import (
	"context"
	"fmt"
)

const (
	featureFlagPath = "feature_flag"
)

// WatchFeatureFlags : 监听特性开关变更
func WatchFeatureFlags(ctx context.Context) (<-chan any, error) {
	return WatchChange(ctx, GetFeatureFlagsPath())
}

// GetFeatureFlagsPath 获取特性开关的 consul 存储地址
func GetFeatureFlagsPath() string {
	return fmt.Sprintf("%s/%s/%s", basePath, dataPath, featureFlagPath)
}

// GetFeatureFlags : 获取特性开关配置
func GetFeatureFlags() ([]byte, error) {
	return GetKVData(GetFeatureFlagsPath())
}
