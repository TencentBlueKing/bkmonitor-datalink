// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"context"
	"errors"
	"net/http"

	promPromql "github.com/prometheus/prometheus/promql"
)

// causedError 是一段已经拼好的错误文本，同时保留产生它的原始错误。
type causedError struct {
	text   string
	causes []error
}

func (e *causedError) Error() string { return e.text }

func (e *causedError) Unwrap() []error { return e.causes }

// ErrorWithCauses 返回文本为 text 的错误，并保留 causes 作为它的原因链：
// 多路查询把各路错误拼成一段文本时，用它代替 errors.New(text)，文本不变，
// errors.Is / errors.As 仍能读到其中任一路的原始错误（例如超时）。
func ErrorWithCauses(text string, causes ...error) error {
	kept := make([]error, 0, len(causes))
	for _, cause := range causes {
		if cause != nil {
			kept = append(kept, cause)
		}
	}
	return &causedError{text: text, causes: kept}
}

// FailureStatus 是查询失败时回给调用方的 HTTP 状态码，所有失败响应共用。
// 存储或查询引擎在截止时间内没有应答是服务端超时，回 504，调用方据此区分
// "请求本身有问题"（400）与"后端没来得及答"；调用方主动取消（context.Canceled）不是超时，仍回 400。
func FailureStatus(err error) int {
	var engineTimeout promPromql.ErrQueryTimeout
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &engineTimeout) {
		return http.StatusGatewayTimeout
	}
	return http.StatusBadRequest
}
