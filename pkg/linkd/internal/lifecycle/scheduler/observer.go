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
	"context"

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
)

// Observer 接收 Scheduler 的低基数运行结果，不得阻塞或改变处理语义。
type Observer interface {
	LeaseOperation(ctx context.Context, operation, outcome string)
	MailboxOperation(ctx context.Context, eventSourceID, operation, outcome string)
	EventProcessed(ctx context.Context, eventSourceID string, action domain.EventAction, result lifecycle.ProcessResult, err error)
	MailboxDrained(ctx context.Context, eventSourceID, outcome string, events int)
}

type noopObserver struct{}

func (noopObserver) LeaseOperation(context.Context, string, string) {}

func (noopObserver) MailboxOperation(context.Context, string, string, string) {}

func (noopObserver) EventProcessed(context.Context, string, domain.EventAction, lifecycle.ProcessResult, error) {
}

func (noopObserver) MailboxDrained(context.Context, string, string, int) {}
