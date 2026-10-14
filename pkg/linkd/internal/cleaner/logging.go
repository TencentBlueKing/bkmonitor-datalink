// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cleaner

import "context"

func (r *Runtime) logEntryFailure(ctx context.Context, entry *cleanerEntry, operation, reason string, err error) {
	if r.logger == nil {
		return
	}
	tenant := entry.event.BKTenantID
	if tenant == "" {
		tenant = entry.delivery.Message.TenantID
	}
	source := entry.event.EventSourceID
	if source == "" {
		source = r.sourceID
	}
	r.logger.WarnContext(ctx, "discard cleaner message", "module", "cleaner", "operation", operation, "error", err,
		"reason_code", reason, "bk_tenant_id", tenant, "event_source_id", source, "event_source_version", r.sourceVersion,
		"event_id", entry.event.EventID, "message_id", entry.delivery.Message.ID, "lane", entry.delivery.Meta.Lane,
		"position", entry.delivery.Meta.Position, "attempts", max(1, entry.processAttempt, entry.storeAttempts, entry.mailboxAttempts))
}
