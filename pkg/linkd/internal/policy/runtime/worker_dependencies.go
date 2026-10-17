// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package runtime

import (
	"context"
	"net/http"
	"net/url"
	"sync"

	"linkd/internal/policy"
	"linkd/internal/taskdispatch"
)

// WorkerDependencies 让同进程来源任务共享依赖，后台读取选择仍在运行且有租户权限的任务。
type WorkerDependencies struct {
	*Dependencies
	mu      sync.Mutex
	readers map[string]workerDependencyTask
	client  taskdispatch.Client
}

type workerDependencyTask struct{ tenant, worker string }

// NewWorkerDependencies 创建 Worker 本地只读副本；实际外部数据由控制面去重读取。
func NewWorkerDependencies(ctx context.Context, client taskdispatch.Client) *WorkerDependencies {
	w := &WorkerDependencies{readers: map[string]workerDependencyTask{}, client: client}
	w.Dependencies = NewDependencies(ctx, w.read)
	return w
}

// Register 在来源任务开始时注册其授权范围，返回退出时的注销函数。
func (w *WorkerDependencies) Register(task taskdispatch.Task, tenant string) func() {
	w.mu.Lock()
	w.readers[task.ID] = workerDependencyTask{tenant: tenant, worker: task.Worker}
	w.mu.Unlock()
	return func() { w.mu.Lock(); delete(w.readers, task.ID); w.mu.Unlock() }
}

func (w *WorkerDependencies) read(ctx context.Context, request DependencyRequest) (DependencySnapshot, error) {
	reader, err := w.reader(request.TenantID)
	if err != nil {
		return DependencySnapshot{}, err
	}
	q := url.Values{"task": {reader.TaskID}, "bk_tenant_id": {request.TenantID}}
	var value DependencySnapshot
	client := reader.Client
	client.MaxResponseBytes = 33 << 20
	err = client.Call(ctx, http.MethodPost, "/internal/policy-dependencies?"+q.Encode(), request, &value)
	if err != nil {
		return DependencySnapshot{}, workerPolicyError(err)
	}
	return value, nil
}

// validateDependencySnapshot 在 cache 装载时复核作用域与成员，不在逐 Event 查询重扫。
func validateDependencySnapshot(request DependencyRequest, value DependencySnapshot) error {
	switch request.Kind {
	case "scope":
		if value.Scope.TenantID != request.TenantID || value.Scope.SpaceCode != request.SpaceCode {
			return policy.ErrAccess
		}
	case "targets":
		if value.Targets.Scope.TenantID != request.TenantID || value.Targets.Scope.SpaceCode != request.SpaceCode {
			return policy.ErrAccess
		}
		if request.Target == nil {
			return policy.ErrInvalid
		}
		for _, ref := range value.Targets.Instances {
			if ref.ModelID != request.Target.ModelID || ref.EntityUID != ref.ModelID+"|"+ref.InstanceID {
				return policy.ErrTargetIdentity
			}
		}
	case "relations":
		for _, ref := range value.Related {
			if ref.ModelID != request.ModelID {
				return policy.ErrAccess
			}
			if ref.EntityUID != ref.ModelID+"|"+ref.InstanceID {
				return policy.ErrTargetIdentity
			}
		}
	default:
		return policy.ErrInvalid
	}
	return nil
}

func (w *WorkerDependencies) reader(tenant string) (WorkerReader, error) {
	w.mu.Lock()
	taskID, worker := "", ""
	for id, t := range w.readers {
		if t.tenant == "" || t.tenant == tenant {
			taskID, worker = id, t.worker
			break
		}
	}
	w.mu.Unlock()
	if taskID == "" {
		return WorkerReader{}, policy.ErrUnavailable
	}
	client := w.client
	client.WorkerID = worker
	return WorkerReader{Client: client, TaskID: taskID}, nil
}

// List 为进程共享 Catalog 选择仍有效的来源任务身份。
func (w *WorkerDependencies) List(ctx context.Context, scope policy.Scope, after string, limit int) ([]policy.Record, error) {
	r, err := w.reader(scope.TenantID)
	if err != nil {
		return nil, err
	}
	return r.List(ctx, scope, after, limit)
}

// PublishedPolicies 一次读取租户发布集合，避免大量策略按小页重复进行 Worker 授权与网络请求。
func (w *WorkerDependencies) PublishedPolicies(ctx context.Context, tenant string) ([]policy.Release, error) {
	r, err := w.reader(tenant)
	if err != nil {
		return nil, err
	}
	client := r.Client
	client.MaxResponseBytes = 33 << 20
	q := url.Values{"task": {r.TaskID}, "bk_tenant_id": {tenant}}
	var releases []policy.Release
	err = client.Call(ctx, http.MethodGet, "/internal/policy-catalog?"+q.Encode(), nil, &releases)
	return releases, workerPolicyError(err)
}

// GetRelease 读取在途绑定的精确版本，不受新事件候选索引限制。
func (w *WorkerDependencies) GetRelease(ctx context.Context, scope policy.Scope, id string, version int64) (policy.Release, error) {
	r, err := w.reader(scope.TenantID)
	if err != nil {
		return policy.Release{}, err
	}
	return r.GetRelease(ctx, scope, id, version)
}
