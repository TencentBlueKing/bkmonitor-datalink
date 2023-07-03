// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package time

import (
	"time"

	"github.com/prometheus/common/model"
)

// ParseDuration 原生ParseDuration方法不支持d，使用Prom封装的方法，增加对：d，w，y等支持
func ParseDuration(s string) (time.Duration, error) {
	d, err := model.ParseDuration(s)
	td := time.Duration(d)
	return td, err
}
