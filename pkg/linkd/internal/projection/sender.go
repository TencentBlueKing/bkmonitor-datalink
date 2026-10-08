// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package projection

import "context"

// Destination 标识已授权的内置兼容存储目标；连接由装配方直接注入写入器。
type Destination struct{ TargetID string }

// Failure 仅保存安全分类，避免 URL、认证或远端响应体进入持久任务与日志。
type Failure struct {
	// Code 是固定的无敏感信息失败分类。
	Code string
	// Retryable 指示本次故障是否可以在自动预算内重试。
	Retryable bool
}

func (f Failure) Error() string { return "projection delivery: " + f.Code }

func validFailureCode(code string) bool {
	switch code {
	case "transport_failed", "response_too_large", "response_invalid", "remote_unavailable", "remote_rejected", "remote_unauthorized", "revision_conflict", "visibility_pending", "attempt_interrupted", "target_unavailable":
		return true
	default:
		return false
	}
}

// Sender 负责一次有界兼容写入；重试策略属于持久任务，不能在适配器内隐藏无限重试。
type Sender interface {
	Send(context.Context, Destination, Request) (Receipt, error)
}
