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
	"net"
	"net/http"
	"strconv"

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

// BackendStatusError 是存储后端以 HTTP 状态码答复的失败：Status 是后端自己的状态码，
// 由发出请求的一方按协议字段写入，不从错误文本推断。
type BackendStatusError struct {
	Status int
}

func (e *BackendStatusError) Error() string {
	return "backend answered http status " + strconv.Itoa(e.Status)
}

// FailureStatus 是查询失败时回给调用方的 HTTP 状态码，所有失败响应共用，只按错误链里的类型判断，不匹配文本：
//   - 504：存储或查询引擎在截止时间内没有应答（context.DeadlineExceeded、查询引擎的 ErrQueryTimeout）；
//   - 502：后端本身失败，与请求无关、调用方也改不了：非超时的网络或传输错误（连接被拒、重置、DNS 等，
//     包括本服务自己的 Redis、Consul 等依赖），后端答复 5xx，或后端答复 401/403（本服务访问后端的凭据或配置）；
//   - 400：其余一切，包括请求校验与解析错误、后端答复的其他 4xx、调用方主动取消（context.Canceled），
//     以及只在应答正文里说明失败、没有类型化原因的情况。
func FailureStatus(err error) int {
	var engineTimeout promPromql.ErrQueryTimeout
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &engineTimeout) {
		return http.StatusGatewayTimeout
	}
	if backendFailed(err) {
		return http.StatusBadGateway
	}
	return http.StatusBadRequest
}

// backendFailed 遍历整条错误链（含多路合并的每一路），有一路是后端本身的失败即为真。
func backendFailed(err error) bool {
	if err == nil {
		return false
	}
	var status *BackendStatusError
	if errors.As(err, &status) && status != nil {
		if status.Status >= http.StatusInternalServerError || status.Status == http.StatusUnauthorized || status.Status == http.StatusForbidden {
			return true
		}
	}
	if transportFailed(err) {
		return true
	}
	switch unwrapped := err.(type) {
	case interface{ Unwrap() []error }:
		for _, cause := range unwrapped.Unwrap() {
			if backendFailed(cause) {
				return true
			}
		}
	case interface{ Unwrap() error }:
		return backendFailed(unwrapped.Unwrap())
	}
	return false
}

// transportFailed 是这一层错误本身是非超时的网络错误；调用方取消的请求不算。
func transportFailed(err error) bool {
	transport, ok := err.(net.Error)
	return ok && !transport.Timeout() && !errors.Is(err, context.Canceled)
}
