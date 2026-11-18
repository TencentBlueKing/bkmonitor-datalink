// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package victoriaMetrics

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// vmRequestResult 是一次 VM 请求的最终结果：HTTP 层失败时就是那个错误；HTTP 层成功时，网关可能在
// 应答体里报失败（result 为假，或者 code 不是 OK），这时返回 BackendBodyError。
func vmRequestResult(err error, data any) error {
	if err != nil {
		return err
	}
	var (
		result        bool
		code, message string
	)
	switch resp := data.(type) {
	case *VmResponse:
		result, code, message = resp.Result, resp.Code, resp.Message
	case *VmLableValuesResponse:
		result, code, message = resp.Result, resp.Code, resp.Message
	case *VmSeriesResponse:
		result, code, message = resp.Result, resp.Code, resp.Message
	default:
		return nil
	}
	if result && code == OK {
		return nil
	}
	return &metadata.BackendBodyError{Code: code, Message: message}
}
