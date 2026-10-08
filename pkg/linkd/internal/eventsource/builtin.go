// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package eventsource 管理来源配置及不可变发布；执行所有权由调度器负责。
package eventsource

import (
	"context"
	"errors"
	"fmt"

	"linkd/internal/config"
	"linkd/internal/domain"
)

// EnsureMergeSource 只在不存在时发布空 Enrich/Hook 的内置来源；保留用户停用、删除和后续配置。
// 与普通来源共用不可变 Release/CAS，调用者必须检查 Enabled/Deleted 后再创建新的内部 Event。
func (s *Service) EnsureMergeSource(ctx context.Context, actor string) (Record, error) {
	r, err := s.Get(ctx, domain.BuiltinMergeEventSourceID)
	if errors.Is(err, ErrNotFound) {
		spec := config.EventSource{EventSourceID: domain.BuiltinMergeEventSourceID, Enabled: true, Storage: config.EventSourceStorageConfig{Type: config.StorageTypeInternalMerge}}
		r, err = s.Apply(ctx, spec, 0, false, actor)
		if errors.Is(err, ErrConflict) {
			r, err = s.Get(ctx, domain.BuiltinMergeEventSourceID)
		}
	}
	if err != nil {
		return Record{}, err
	}
	if r.Spec.Storage.Type != config.StorageTypeInternalMerge {
		return Record{}, fmt.Errorf("reserved merge source has an incompatible input type")
	}
	if r.Pending != nil {
		return s.Recover(ctx, r.ID)
	}
	return r, nil
}
