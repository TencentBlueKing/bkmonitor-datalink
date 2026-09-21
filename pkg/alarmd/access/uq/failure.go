// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

// These errors retain existing error text; only QueryFailure is safe to log.
// They do not change provider completeness, retry or cancellation behavior.
//
// Deterministic UQ status codes and absent identity dimensions are no longer
// Go errors: a status code completes the physical query as UNAVAILABLE with a
// bounded "response=status_<code>" route detail (see decode), and an absent
// identity dimension is bound to JSON null like Python binds it to None (see
// normalizeSeries).

// responseLimitError is a client-side response budget violation. It keeps the
// historical sentinel text so errors.Is against the exported variables keeps
// working, and exposes the budget name as a stable diagnostic code.
type responseLimitError struct{ code string }

func (e *responseLimitError) Error() string {
	return "alarmd access uq: " + e.code
}
func (e *responseLimitError) QueryFailure() (string, string) {
	return "budget", e.code
}
