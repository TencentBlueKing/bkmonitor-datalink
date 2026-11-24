// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package merge

import (
	"context"
	"fmt"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/mailbox"
	"linkd/internal/policy"
	"linkd/internal/store"
)

// EventCreator 是内部生产者使用的 create-only Event 端口，必须支持已处理事件的事实幂等重投。
type EventCreator interface {
	CreateEvent(context.Context, domain.Event) (store.CreateEventResult, error)
}

// Mailboxes 使用与 Cleaner 相同的持久化后入队契约，但不调用 Cleaner 或外部消息队列。
type Mailboxes interface {
	EnqueueBatch(context.Context, []domain.Event) ([]mailbox.EnqueueResult, error)
}

// Publisher 只投递已写入 Journal 的父 Event；不会重新选择配置版本、生成身份或渲染模板。
type Publisher struct {
	Journal   *Journal
	Events    EventCreator
	Mailboxes Mailboxes
}

// Submit 先持久化父 Event，再入 Mailbox，最后确认阶段；任一步失败重试同一 Event。
// Mailbox 接受重复稳定 EventID，Lifecycle 已完成结果阻止重复业务处理，不声称跨存储事务。
func (p *Publisher) Submit(ctx context.Context, tenant, id string, at time.Time) (StoredDecision, error) {
	if p.Journal == nil || p.Events == nil || p.Mailboxes == nil {
		return StoredDecision{}, policy.ErrInvalid
	}
	current, err := p.Journal.Get(ctx, tenant, id)
	if err != nil {
		return StoredDecision{}, err
	}
	if current.Decision.Progress.Phase != "prepared" {
		switch current.Decision.Progress.Phase {
		case "waiting_parent", "linking", "ending", "completed", "releasing":
			return current, nil
		default:
			return StoredDecision{}, policy.ErrConflict
		}
	}
	event := current.Decision.Progress.ParentEvent.Clone()
	created, err := p.Events.CreateEvent(ctx, event)
	if err != nil {
		return StoredDecision{}, err
	}
	if created.Version.IsZero() || created.Event.Validate() != nil {
		return StoredDecision{}, policy.ErrInvalid
	}
	if err := domain.ValidateEventRedelivery(event, created.Event); err != nil {
		return StoredDecision{}, policy.ErrConflict
	}
	if created.Event.BKTenantID != tenant || created.Event.EventID != event.EventID {
		return StoredDecision{}, policy.ErrAccess
	}
	if created.Processing.State == domain.EventProcessStateUnprocessed {
		results, err := p.Mailboxes.EnqueueBatch(ctx, []domain.Event{created.Event})
		if err != nil {
			return StoredDecision{}, err
		}
		if len(results) != 1 {
			return StoredDecision{}, fmt.Errorf("internal mailbox returned incomplete enqueue result")
		}
		if results[0].Err != nil {
			return StoredDecision{}, results[0].Err
		}
	}
	return p.Journal.MarkEnqueued(ctx, current, at)
}
