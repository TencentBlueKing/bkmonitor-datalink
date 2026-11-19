// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bksql

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// bkSQLRequestResult 是一次 bk_sql 请求的最终结果：HTTP 层失败时就是那个错误；HTTP 层成功时，网关可能
// 在应答体里报失败（result 为假，或者 code 不是成功），这时返回 BackendBodyError。
func bkSQLRequestResult(err error, res *Result) error {
	if err != nil || res == nil {
		return err
	}
	if res.Result && res.Code == StatusOK {
		return nil
	}
	message := res.Message
	if res.Errors.Error != "" {
		message = res.Errors.Error
	}
	return &metadata.BackendBodyError{Code: res.Code, Message: message}
}
