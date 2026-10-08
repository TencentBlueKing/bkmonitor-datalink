// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplaneprocess

import (
	"context"
	"errors"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/policy"
	policyruntime "linkd/internal/policy/runtime"
	"linkd/internal/runtimeconfig"
	"linkd/internal/store"
	storeassembly "linkd/internal/store/assembly"
)

type policyPreviewFacts struct{ storage config.StorageConfig }

func (f policyPreviewFacts) Event(ctx context.Context, tenant, id string) (domain.Event, error) {
	r, err := storeassembly.OpenReadOnly(ctx, f.storage, 4)
	if err != nil {
		return domain.Event{}, err
	}
	defer func() { _ = r.Close() }()
	saved, err := r.Repository.GetEvent(ctx, tenant, id)
	if errors.Is(err, store.ErrNotFound) {
		err = policy.ErrNotFound
	}
	return saved.Event, err
}

func (f policyPreviewFacts) Alert(ctx context.Context, tenant, id string) (domain.Alert, error) {
	r, err := storeassembly.OpenReadOnly(ctx, f.storage, 4)
	if err != nil {
		return domain.Alert{}, err
	}
	defer func() { _ = r.Close() }()
	saved, err := r.Repository.GetAlert(ctx, tenant, id)
	if errors.Is(err, store.ErrNotFound) {
		err = policy.ErrNotFound
	}
	return saved.Alert, err
}

func newPolicyPreview(service *policy.Service, storage config.StorageConfig, runtime *policyruntime.Runtime, severity *runtimeconfig.Severity) *policy.Previewer {
	return policy.NewPreviewer(service, policyPreviewFacts{storage}, runtime.Targets, runtime.Relations, func() policy.LevelMapper { snapshot := severity.SeveritySnapshot(); return snapshot.KACLevel })
}
