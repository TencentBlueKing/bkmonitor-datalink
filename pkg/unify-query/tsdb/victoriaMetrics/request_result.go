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

// vmAnswer 是 vmQuery 能解码的应答：每种应答都要给出体内的 result 与 code，体内报的失败才不会被算成成功。
// vmQuery 只收这个接口，新加一种应答类型却没实现它，就编译不过。
type vmAnswer interface {
	bodyStatus() (result bool, code, message string)
}

func (r *VmResponse) bodyStatus() (bool, string, string) { return r.Result, r.Code, r.Message }

func (r *VmLableValuesResponse) bodyStatus() (bool, string, string) {
	return r.Result, r.Code, r.Message
}

func (r *VmSeriesResponse) bodyStatus() (bool, string, string) { return r.Result, r.Code, r.Message }

// vmRequestResult 是一次 VM 请求的最终结果：HTTP 层失败时就是那个错误；HTTP 层成功时，网关可能在
// 应答体里报失败（result 为假，或者 code 不是 OK），这时返回 BackendBodyError。
func vmRequestResult(err error, data vmAnswer) error {
	if err != nil {
		return err
	}
	result, code, message := data.bodyStatus()
	if result && code == OK {
		return nil
	}
	return &metadata.BackendBodyError{Code: code, Message: message}
}
