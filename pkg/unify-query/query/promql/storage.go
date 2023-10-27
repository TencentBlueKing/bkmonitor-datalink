// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promql

import (
	"context"
	"time"

	"github.com/prometheus/prometheus/storage"
)

// InfluxDBStorage
type InfluxDBStorage struct {
	// 最大的时间查询范围
	maxTimeRange time.Duration
}

// Querier: 返回查询对象，在prometheus中是返回对应可以覆盖查询范围的查询对象
// 在查询模块中，我们可以考虑做以下的内容：
// 1. 判断查询范围是否有超出influxdb的范围，如果超过，则提供数据平台降采样的数据
// 目前都是返回统一的查询client
func (i *InfluxDBStorage) Querier(ctx context.Context, mint, maxt int64) (storage.Querier, error) {
	var (
		now       = time.Now()
		startTime = time.Unix(mint/1000, 0)
		endTime   = time.Unix(maxt/1000, 0)
	)

	// 如果maxTimeRange为0，可以接受任何时间范围的查询
	// 否则判断时间范围是否超过 1. mint过早，2. maxt超过现在时间
	if i.maxTimeRange != 0 && (now.Sub(startTime) > i.maxTimeRange || endTime.Sub(now) > 0) {
		return nil, ErrTimeRangeTooLarge
	}

	return NewInfluxdbQuerier(ctx)
}
