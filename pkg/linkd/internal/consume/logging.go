// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consume

import (
	"context"
	"log/slog"
)

// WithLogger 注入消费调度和确认失败日志；处理器自行记录的业务丢弃不重复打印。
func WithLogger(logger *slog.Logger) RuntimeOption {
	return func(runtime *Runtime) { runtime.logger = logger }
}

func (s *runtimeState) logDiscard(entry *trackedDelivery, reason string, err error) {
	if s.runtime.logger == nil {
		return
	}
	s.runtime.logger.WarnContext(context.Background(), "discard consumer message", "module", "consume", "operation", "handle",
		"error", err, "reason_code", reason, "bk_tenant_id", entry.delivery.Message.TenantID,
		"event_source_id", s.runtime.labels.EventSourceID, "message_id", entry.delivery.Message.ID,
		"lane", entry.delivery.Meta.Lane, "position", entry.delivery.Meta.Position, "attempts", entry.attempt)
}
