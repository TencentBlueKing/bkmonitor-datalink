// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// influxRequestResult 是一次 InfluxDB HTTP 请求的最终结果：HTTP 层失败时就是那个错误；HTTP 层成功时，
// InfluxDB 1.x 会在应答体里报失败（顶层的 error，或者某条语句的 error），这时返回 BackendBodyError。
func influxRequestResult(err error, res *decoder.Response) error {
	if err != nil || res == nil {
		return err
	}
	if res.Err != "" {
		return &metadata.BackendBodyError{Message: res.Err}
	}
	for _, result := range res.Results {
		if result.Err != "" {
			return &metadata.BackendBodyError{Message: result.Err}
		}
	}
	return nil
}
