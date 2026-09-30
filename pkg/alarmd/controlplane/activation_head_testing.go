// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"context"
	"encoding/json"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// WriteActivationForTest writes an activation header and body as a later
// build would, bypassing every check this build's writers make: a head body
// (schema v3, no Plans), or one carrying cutover progress. Tests use it to
// stand in the state a rollback lands this build on.
func WriteActivationForTest(ctx context.Context, repository *RedisCatalogRepository, state ActivationState) error {
	header, err := activationHeader(state.RecordRevision, state.Current, state.Pending)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := repository.client.Set(ctx, repository.activationHeaderKey(), header, 0).Err(); err != nil {
		return err
	}
	return repository.client.Set(ctx, repository.activationKey(), payload, 0).Err()
}

// PersistActiveQueryGroupSetForTest writes an active Query Group set the way
// a cutover does and returns its reference.
func PersistActiveQueryGroupSetForTest(
	ctx context.Context, repository *RedisCatalogRepository, identities []execution.QueryGroupIdentity,
) (ActiveQueryGroupSetRef, error) {
	ref, _, err := repository.persistAndVerifyActiveQGSet(ctx, identities)
	return ref, err
}

// ActivationHeadSchemaVersionForTest is the schema a head body carries.
const ActivationHeadSchemaVersionForTest = activationHeadSchemaVersion
