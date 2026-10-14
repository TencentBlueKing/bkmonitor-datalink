// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package projection

import (
	"context"
	"errors"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

type existingProjectionStore struct {
	Store
	tenant, id string
}

func (s *existingProjectionStore) Exists(_ context.Context, tenant, id string) (bool, error) {
	s.tenant, s.id = tenant, id
	return true, nil
}

func TestKnownCandidateDoesNotReadAlertOrRebuildTask(t *testing.T) {
	tasks := &existingProjectionStore{}
	service := &Service{tasks: tasks} // 其他依赖故意不装配，已存在路径不得调用它们。
	a := storetest.Alert("tenant", "alert", "event", "fp", "warning")
	a.Projection = domain.AlertProjection{Targets: map[string]domain.ProjectionTargetState{"kac": {SourceVersion: 1, RequiredRevision: 1}}}
	_, err := service.ProduceCandidate(t.Context(), store.StoredAlert{Alert: a}, "kac")
	id, _ := TaskID("tenant", "alert", "kac", 1)
	if !errors.Is(err, errAlreadyProduced) || tasks.tenant != "tenant" || tasks.id != id {
		t.Fatal("existing task was reconstructed", err, tasks)
	}
}
