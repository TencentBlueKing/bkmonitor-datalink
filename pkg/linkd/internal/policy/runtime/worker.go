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
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"linkd/internal/policy"
	"linkd/internal/taskdispatch"
)

// WorkerReader 通过现有 Worker 身份和明确任务读取当前租户策略；不使用管理 JWT。
type WorkerReader struct {
	Client taskdispatch.Client
	TaskID string
}

// List 返回最多三条已发布记录，控制面会移除 Pending 配置。
func (r WorkerReader) List(ctx context.Context, scope policy.Scope, after string, limit int) ([]policy.Record, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 3 || r.TaskID == "" {
		return nil, policy.ErrInvalid
	}
	query := url.Values{"task": {r.TaskID}, "bk_tenant_id": {scope.TenantID}, "type": {string(scope.Kind)}, "after": {after}, "limit": {strconv.Itoa(limit)}}
	var rows []policy.Record
	err := r.Client.Call(ctx, http.MethodGet, "/internal/policies?"+query.Encode(), nil, &rows)
	return rows, workerPolicyError(err)
}

// GetRelease 读取已冻结裁决引用的精确版本；不存在时不以最新配置回退。
func (r WorkerReader) GetRelease(ctx context.Context, scope policy.Scope, id string, version int64) (policy.Release, error) {
	if err := scope.Validate(); err != nil {
		return policy.Release{}, err
	}
	if version < 1 || r.TaskID == "" {
		return policy.Release{}, policy.ErrInvalid
	}
	query := url.Values{"task": {r.TaskID}, "bk_tenant_id": {scope.TenantID}}
	var release policy.Release
	err := r.Client.Call(ctx, http.MethodGet, "/internal/policies/"+url.PathEscape(string(scope.Kind))+"/"+url.PathEscape(id)+"/releases/"+strconv.FormatInt(version, 10)+"?"+query.Encode(), nil, &release)
	if err == nil && (release.Scope != scope || release.ID != id || release.Version != version) {
		return policy.Release{}, policy.ErrAccess
	}
	return release, workerPolicyError(err)
}

func workerPolicyError(err error) error {
	var response *taskdispatch.ResponseError
	if errors.As(err, &response) {
		switch response.StatusCode {
		case http.StatusBadRequest:
			return errors.Join(policy.ErrInvalid, err)
		case http.StatusUnauthorized, http.StatusForbidden:
			return errors.Join(policy.ErrAccess, err)
		case http.StatusNotFound:
			return errors.Join(policy.ErrNotFound, err)
		}
	}
	return err
}
