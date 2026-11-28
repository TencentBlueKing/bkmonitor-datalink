// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"errors"
	"sync"

	"linkd/internal/pluginruntime"
)

// ErrExecutionBusy 表示本进程正在处理同一租户/来源/接收身份；调用方不得并发重放。
var ErrExecutionBusy = errors.New("source request is already executing")

type executionIdentity struct{ tenant, source, request string }

// Executions 登记本进程正在执行的推送请求，共用于同步接收和后台恢复。
// 它不排队、不持久化历史、不提供跨进程授权；跨进程写入依赖 Redis 中已有领导权。
type Executions struct {
	mu     sync.Mutex
	active map[executionIdentity]bool
}

func (e *Executions) begin(request pluginruntime.Request) (func(), bool) {
	identity := executionIdentity{request.TenantID, request.SourceID, request.RequestID}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.active[identity] {
		return nil, false
	}
	if e.active == nil {
		e.active = make(map[executionIdentity]bool)
	}
	e.active[identity] = true
	return func() { e.mu.Lock(); delete(e.active, identity); e.mu.Unlock() }, true
}

// WithExecutions 返回使用同一请求登记的服务副本；须在服务开始使用前装配。
func (s *Service) WithExecutions(executions *Executions) *Service {
	copy := *s
	copy.executions = executions
	return &copy
}
