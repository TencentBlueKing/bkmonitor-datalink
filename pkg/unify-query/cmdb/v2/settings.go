// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v2

const (
	MaxHopsConfigPath              = "cmdb.v2.max_hops"
	MaxAllowedHopsConfigPath       = "cmdb.v2.max_allowed_hops"
	DefaultLimitConfigPath         = "cmdb.v2.default_limit"
	DefaultLookBackDeltaConfigPath = "cmdb.v2.look_back_delta"
)

var (
	DefaultMaxHops       = 2
	MaxAllowedHops       = 5
	DefaultLimit         = 100
	DefaultLookBackDelta = int64(86400000) // 24小时（毫秒）
)
