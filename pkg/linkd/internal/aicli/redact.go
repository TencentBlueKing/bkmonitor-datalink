// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package aicli

import (
	"encoding/base64"
	"regexp"
	"strings"
)

// redactor 只在输出边界工作；绝不修改实际请求或配置中保存的认证材料。
type redactor struct{ secrets []string }

func connectionRedactor(p profile) redactor {
	return redactor{secrets: []string{p.Password, base64.StdEncoding.EncodeToString([]byte(p.Username + ":" + p.Password))}}
}

func sensitiveKey(key string) bool {
	key = strings.ToLower(strings.NewReplacer("-", "_", " ", "_").Replace(key))
	compact := strings.ReplaceAll(key, "_", "")
	for _, name := range []string{"password", "passwd", "token", "authorization", "apikey", "secret", "clientsecret", "privatekey", "clientkey", "credentials", "keypem"} {
		if compact == name || strings.HasSuffix(compact, name) {
			return true
		}
	}
	return false
}

func (r redactor) text(value string) string {
	for _, secret := range r.secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "******")
		}
	}
	// 嵌套资源地址可能包含 userinfo；即使后端漏脱敏，也不把凭据送入 AI 上下文。
	value = regexp.MustCompile(`(?i)(https?://)[^\s/?#@]+@`).ReplaceAllString(value, "${1}******@")
	return value
}

func (r redactor) value(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, value := range v {
			if sensitiveKey(key) {
				out[key] = "******"
			} else {
				out[key] = r.value(value)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, value := range v {
			out[i] = r.value(value)
		}
		return out
	case string:
		return r.text(v)
	default:
		return value
	}
}
