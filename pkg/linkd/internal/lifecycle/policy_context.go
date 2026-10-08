// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle

import (
	"context"
	"fmt"

	"linkd/internal/store"
)

func (p *Processor) freezePolicies(ctx context.Context, stored store.StoredEvent) (store.StoredEvent, error) {
	if p.policies == nil || stored.Processing.PolicyContext != nil {
		return stored, nil
	}
	at, err := p.now()
	if err != nil {
		return store.StoredEvent{}, err
	}
	snapshot, err := p.policies.Snapshot(ctx, stored.Event, at)
	if err != nil {
		return store.StoredEvent{}, err
	}
	if snapshot == nil {
		return store.StoredEvent{}, fmt.Errorf("policy snapshot missing")
	}
	if err := snapshot.Validate(); err != nil {
		return store.StoredEvent{}, err
	}
	if snapshot.ReasonCode != "" {
		p.logger.WarnContext(ctx, "event policies skipped", "bk_tenant_id", stored.Event.BKTenantID, "event_id", stored.Event.EventID, "reason", snapshot.ReasonCode)
	}
	return p.writeEventResult(ctx, stored, store.EventResult{State: stored.Processing.State, PolicyContext: snapshot})
}
