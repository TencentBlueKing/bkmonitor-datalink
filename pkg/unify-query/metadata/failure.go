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
	"net/url"
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

// BackendBodyError 是后端以 2xx 答复、却在应答体里报的失败（结果标志为假，或者业务码不是成功），
// 由读应答体的一方按协议字段写入。业务码的含义还没有分类，失败响应仍按其余情况归类；
// 存储请求计数把它单列为 body_error。
type BackendBodyError struct {
	Code    string
	Message string
}

func (e *BackendBodyError) Error() string {
	if e.Code == "" {
		return "backend reported a failure in the response body: " + e.Message
	}
	return "backend reported a failure in the response body (code " + e.Code + "): " + e.Message
}

// BackendResponseError 是读或解码后端应答体失败：应答已经开始，后端给的应答不完整或不成形
// （例如读到一半连接断开），按后端失败归类。只由读后端应答的一层包装，调用方请求体的解码失败不经过它。
type BackendResponseError struct {
	Err error
}

func (e *BackendResponseError) Error() string { return e.Err.Error() }

func (e *BackendResponseError) Unwrap() error { return e.Err }

// FailureStatus 是查询失败时回给调用方的 HTTP 状态码，所有失败响应共用，只按错误链里的类型判断，不匹配文本：
//   - 504：存储或查询引擎在截止时间内没有应答（context.DeadlineExceeded、网络层的超时 net.Error.Timeout()，
//     如读写截止时间到了的 i/o timeout，以及查询引擎的 ErrQueryTimeout）；
//   - 502：后端本身失败，与请求无关、调用方也改不了：非超时的网络或传输错误（连接被拒、重置、DNS 等，
//     包括本服务自己的 Redis、Consul 等依赖），读后端应答体失败（BackendResponseError），后端答复 5xx、429（后端自身的负载），
//     或 401/403（本服务访问后端的凭据或配置）；
//   - 400：其余一切，包括请求校验与解析错误、后端答复的其他 4xx、调用方主动取消（context.Canceled），
//     以及只在应答正文里说明失败、没有类型化原因的情况。
func FailureStatus(err error) int {
	var engineTimeout promPromql.ErrQueryTimeout
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &engineTimeout) || networkTimedOut(err) {
		return http.StatusGatewayTimeout
	}
	if backendFailed(err) {
		return http.StatusBadGateway
	}
	return http.StatusBadRequest
}

// networkTimedOut 遍历整条错误链（含多路合并的每一路），有一层是网络层的超时（net.Error.Timeout()）即为真。
// 不用 errors.As：它停在第一个 net.Error 上，多路查询里先遇到的可能是另一路非超时的网络错误。
func networkTimedOut(err error) bool {
	if err == nil {
		return false
	}
	if network, ok := err.(net.Error); ok && network.Timeout() {
		return true
	}
	switch unwrapped := err.(type) {
	case interface{ Unwrap() []error }:
		for _, cause := range unwrapped.Unwrap() {
			if networkTimedOut(cause) {
				return true
			}
		}
	case interface{ Unwrap() error }:
		return networkTimedOut(unwrapped.Unwrap())
	}
	return false
}

// backendFailed 遍历整条错误链（含多路合并的每一路），有一路是后端本身的失败即为真。
func backendFailed(err error) bool {
	if err == nil {
		return false
	}
	var status *BackendStatusError
	if errors.As(err, &status) && status != nil {
		if status.Status >= http.StatusInternalServerError || status.Status == http.StatusUnauthorized ||
			status.Status == http.StatusForbidden || status.Status == http.StatusTooManyRequests {
			return true
		}
	}
	if transportFailed(err) {
		return true
	}
	if response, ok := err.(*BackendResponseError); ok && response != nil {
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

// transportFailed 是这一层错误本身是非超时的网络错误；调用方取消的请求、地址解析失败（*url.Error 的 parse）不算。
func transportFailed(err error) bool {
	if requestErr, ok := err.(*url.Error); ok && requestErr.Op == "parse" {
		return false
	}
	transport, ok := err.(net.Error)
	return ok && !transport.Timeout() && !errors.Is(err, context.Canceled)
}
