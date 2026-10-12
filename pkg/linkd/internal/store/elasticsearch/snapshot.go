// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearchstore

import (
	"fmt"

	"linkd/internal/store"
)

// 快照只来自受信读取/写入；这里绑定租户、实体和不透明 ES token，拒绝跨对象或跨后端复用。
// 本地状态检查不能替代 ES 的最终条件写入，两者必须同时保留。
func eventSnapshotVersion(current store.StoredEvent) (versionPayload, error) {
	if err := current.Validate(); err != nil {
		return versionPayload{}, fmt.Errorf("%w: invalid event snapshot: %w", store.ErrInvalidArgument, err)
	}
	version, ok := decodeVersion(current.Version)
	if !ok || version.DocumentID != documentID(current.Event.BKTenantID, current.Event.EventID) {
		return versionPayload{}, store.ErrVersionConflict
	}
	return version, nil
}

func alertSnapshotVersion(current store.StoredAlert) (versionPayload, error) {
	if err := current.Alert.ValidateState(); err != nil {
		return versionPayload{}, fmt.Errorf("%w: invalid alert snapshot: %w", store.ErrInvalidArgument, err)
	}
	if current.Version.IsZero() {
		return versionPayload{}, fmt.Errorf("%w: expected alert version must not be empty", store.ErrInvalidArgument)
	}
	version, ok := decodeVersion(current.Version)
	if !ok || version.DocumentID != documentID(current.Alert.BKTenantID, current.Alert.AlertID) {
		return versionPayload{}, store.ErrVersionConflict
	}
	return version, nil
}

var _ store.LifecycleEventSnapshotStore = (*Repository)(nil)

var _ store.LifecycleAlertSnapshotStore = (*Repository)(nil)
