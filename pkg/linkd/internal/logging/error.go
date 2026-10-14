// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package logging

import (
	"log/slog"
	"net/url"
	"regexp"
)

var (
	errorURL        = regexp.MustCompile(`https?://[^\s"'<>]+`)
	errorDSN        = regexp.MustCompile(`[^\s:@/]+:[^\s@]+@(tcp|unix)\(`)
	errorCredential = regexp.MustCompile(`(?i)(["']?(?:password|passwd|secret|secret_key|token|access_token|refresh_token|api_key|authorization)["']?\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;]+)`)
	errorBearer     = regexp.MustCompile(`(?i)\bBearer\s+[^\s"',;]+`)
)

// 错误保留操作和根因，只移除常见凭据形式；适配器仍不得把请求/响应正文放进错误。
// 在日志边界统一处理，不改变传回业务代码的错误、重试分类或持久化协议。
func redactError(_ []string, attr slog.Attr) slog.Attr {
	if attr.Key != "error" {
		return attr
	}
	var message string
	if err, ok := attr.Value.Any().(error); ok {
		message = err.Error()
	} else if attr.Value.Kind() == slog.KindString {
		message = attr.Value.String()
	} else {
		return attr
	}
	message = errorURL.ReplaceAllStringFunc(message, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return "[invalid URL]"
		}
		u.User = nil
		u.RawQuery, u.Fragment = "", ""
		return u.String()
	})
	message = errorDSN.ReplaceAllString(message, "[credentials]@$1(")
	message = errorBearer.ReplaceAllString(message, "Bearer [redacted]")
	message = errorCredential.ReplaceAllString(message, "$1[redacted]")
	const maxErrorBytes = 8192
	if len(message) > maxErrorBytes {
		message = message[:maxErrorBytes] + " [truncated]"
	}
	return slog.String(attr.Key, message)
}
