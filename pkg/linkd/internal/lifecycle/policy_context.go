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

	"linkd/internal/domain"
	"linkd/internal/store"
)

func (p *Processor) readPolicies(ctx context.Context, event domain.Event) (*store.PolicyContext, error) {
	defer p.measurePhase(phasePolicySnapshot)()
	if p.policies == nil {
		return nil, nil
	}
	at, err := p.now()
	if err != nil {
		return nil, err
	}
	finish := p.measureStep(phasePolicySnapshot, "read")
	snapshot, err := p.policies.Snapshot(ctx, event, at)
	finish()
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, fmt.Errorf("policy snapshot missing")
	}
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	if snapshot.ReasonCode != "" {
		p.logger.WarnContext(ctx, "event policies skipped", "module", "lifecycle", "operation", "read_policies", "bk_tenant_id", event.BKTenantID, "event_id", event.EventID, "reason", snapshot.ReasonCode)
	}
	return snapshot, nil
}
