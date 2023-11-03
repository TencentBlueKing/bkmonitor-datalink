// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package licensechecker

import (
	"time"
)

type Config struct {
	Enabled           bool          `config:"enabled" mapstructure:"enabled"`
	ExpireTime        int64         `config:"expire_time" mapstructure:"expire_time"`                 // 过期时间(UnixTimestamp)
	TolerableExpire   time.Duration `config:"tolerable_expire" mapstructure:"tolerable_expire"`       // 证书容忍期限
	NumNodes          int32         `config:"number_nodes" mapstructure:"number_nodes"`               // 证书节点数
	TolerableNumRatio float64       `config:"tolerable_num_ratio" mapstructure:"tolerable_num_ratio"` // 容忍节点倍数
}
