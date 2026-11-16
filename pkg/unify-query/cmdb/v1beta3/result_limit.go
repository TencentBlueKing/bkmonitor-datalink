// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta3

import (
	"fmt"
	"strings"
)

// topologyGridLimitError 保留原有错误文案，同时提供稳定的超限指标分类。
type topologyGridLimitError struct {
	count int64
	limit int
}

func (e *topologyGridLimitError) Error() string {
	return fmt.Sprintf("topology time grid contains %d points, maximum is %d", e.count, e.limit)
}

func (e *topologyGridLimitError) TruncationReason() string {
	return "max_shared_topology_points"
}

// ResultLimitError 表示请求超出原始输入规格（请求体、批次条数），或单次后端响应
// 超出分批边界。Count 和 Limit 记录实际数量与允许上限。
type ResultLimitError struct {
	Reason string
	Count  int
	Limit  int
}

// Error 返回可直接用于接口错误消息的超限说明；兼容入口的输入规格以 relation 开头。
func (e *ResultLimitError) Error() string {
	subject := "topology"
	if strings.HasPrefix(e.Reason, "max_relation_") {
		subject = "relation"
	}
	return fmt.Sprintf("%s resource limit exceeded: %s (%d > %d)", subject, e.Reason, e.Count, e.Limit)
}

// TruncationReason 返回稳定的超限原因，供 HTTP 层记录指标和 trace。
func (e *ResultLimitError) TruncationReason() string {
	if e == nil {
		return ""
	}
	return e.Reason
}

// UnsplittableResponseError 表示一次读取已按身份拆到单个身份、按时间拆到单个评估点，
// 后端响应仍超过分批边界。原因保持 max_response_bytes；它是终态错误：不再触发任何拆分，
// 兼容入口也不改用下一条候选路径，以免用另一条路径的答案冒充首条命中。
type UnsplittableResponseError struct {
	Metric string
	Limit  int64
	// relation 表示读取来自兼容入口，文案与 ResultLimitError 一样按入口区分。
	relation bool
	cause    error
}

func (e *UnsplittableResponseError) Error() string {
	subject := "topology"
	if e.relation {
		subject = "relation"
	}
	if e.Limit > 0 {
		return fmt.Sprintf("%s resource limit exceeded: max_response_bytes (one evaluation point of %s exceeds the %d-byte backend response boundary)", subject, e.Metric, e.Limit)
	}
	return fmt.Sprintf("%s resource limit exceeded: max_response_bytes (one evaluation point of %s exceeds the backend response size)", subject, e.Metric)
}

// TruncationReason 与可拆分的超限使用同一稳定原因。
func (e *UnsplittableResponseError) TruncationReason() string { return "max_response_bytes" }

// Unwrap 保留后端或分批边界给出的原始错误。
func (e *UnsplittableResponseError) Unwrap() error { return e.cause }
