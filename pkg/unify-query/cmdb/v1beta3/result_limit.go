// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta3

import "fmt"

// ResultLimitError 表示查询结果触发服务端安全上限。
// Count 和 Limit 用于记录实际数量与允许上限，Path 用于定位发生边扩散的关系字段。
type ResultLimitError struct {
	Reason string
	Count  int
	Limit  int
	Path   string
}

// Error 返回可直接用于接口错误消息的超限说明。
func (e *ResultLimitError) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("result limit exceeded: %s returned %d items, maximum is %d", e.Path, e.Count, e.Limit)
	}
	return fmt.Sprintf("result limit exceeded: query returned %d targets, maximum is %d", e.Count, e.Limit)
}

// TruncationReason 返回稳定的机器可读原因，供 HTTP 层生成截断元数据。
func (e *ResultLimitError) TruncationReason() string {
	if e == nil {
		return ""
	}
	return e.Reason
}
