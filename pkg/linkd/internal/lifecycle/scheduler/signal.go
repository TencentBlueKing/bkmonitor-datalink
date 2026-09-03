// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/mailbox"
)

// Signal 是 Mailbox 唤醒协议在 scheduler 包中的别名。
type Signal = mailbox.Signal

// NewSignal 在 scheduler 调用边界从 Event 构造 Mailbox Signal。
func NewSignal(event domain.Event, enqueuedAt time.Time) Signal {
	return mailbox.NewSignal(event.BKTenantID, event.EventSourceID, event.Fingerprint, enqueuedAt)
}

func EncodeSignal(signal Signal) ([]byte, error) { return mailbox.EncodeSignal(signal) }

func DecodeSignal(data []byte) (Signal, error) { return mailbox.DecodeSignal(data) }

// CorrelationKey 返回稳定 Mailbox ID。
func CorrelationKey(bkTenantID, eventSourceID, fingerprint string) string {
	return mailbox.CorrelationKey(bkTenantID, eventSourceID, fingerprint)
}
