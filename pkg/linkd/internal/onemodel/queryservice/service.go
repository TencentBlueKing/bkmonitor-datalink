// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package queryservice 提供与正式丰富工作池隔离的 OneModel 只读调试用例。
package queryservice

import (
	"context"
	"errors"
	"time"

	"linkd/internal/onemodel"
)

// Error 在 HTTP 边界提供安全错误分类，不包含后端响应和连接材料。
type Error struct {
	// Status 是管理接口的 HTTP 状态码。
	Status int
	// Message 可返回浏览器，不包含依赖系统的原始错误。
	Message string
}

func (e *Error) Error() string { return e.Message }

// Pages 是查询服务消费的分页与快照释放端口。
type Pages interface {
	Search(context.Context, string, onemodel.PageQuery) (onemodel.Page, error)
	Close(context.Context, string, string) error
}

// Relations 是关联用例所需的最小读取端口。
type Relations interface {
	Related(context.Context, string, []onemodel.Instance, string, string, onemodel.Query) ([]onemodel.Instance, error)
}

// SearchRequest 指定租户和分页条件。
type SearchRequest struct {
	Tenant string `json:"bk_tenant_id"`
	onemodel.PageQuery
}

// Root 只允许调用方提供实例身份，实例租户由请求统一注入。
type Root struct {
	ModelID    string `json:"model_id"`
	InstanceID string `json:"model_inst_id"`
}

// RelatedRequest 指定起点身份、关系方向和目标模型过滤。
type RelatedRequest struct {
	Tenant    string         `json:"bk_tenant_id"`
	Roots     []Root         `json:"roots"`
	Relation  string         `json:"relation"`
	Direction string         `json:"direction"`
	Query     onemodel.Query `json:"query"`
}

// CloseRequest 释放指定租户的分页快照。
type CloseRequest struct {
	Tenant string `json:"bk_tenant_id"`
	Cursor string `json:"cursor"`
}

// Response 返回业务文档；不会向浏览器暴露物理 attribute_values。
type Response struct {
	Items               []map[string]any `json:"items"`
	NextCursor          string           `json:"next_cursor,omitempty"`
	ElapsedMilliseconds int64            `json:"elapsed_milliseconds"`
}

// Service 为每次请求提供固定超时及独立并发上限，没有等待队列。
type Service struct {
	pages  Pages
	reader Relations
	slots  chan struct{}
}

// New 注入可并发调用的客户端；nil 端口表示部署尚未配置 OneModel。
func New(pages Pages, reader Relations) *Service {
	return &Service{pages: pages, reader: reader, slots: make(chan struct{}, 4)}
}

func (s *Service) acquire(ctx context.Context) (context.Context, func(), error) {
	if s.pages == nil || s.reader == nil {
		return nil, nil, &Error{503, "resources.onemodel is not configured"}
	}
	select {
	case s.slots <- struct{}{}:
	default:
		return nil, nil, &Error{429, "OneModel query capacity exceeded"}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	return ctx, func() { cancel(); <-s.slots }, nil
}

// Search 返回一个快照页，错误不会降级成空结果。
func (s *Service) Search(ctx context.Context, request SearchRequest) (Response, error) {
	ctx, release, err := s.acquire(ctx)
	if err != nil {
		return Response{}, err
	}
	defer release()
	start := time.Now()
	page, err := s.pages.Search(ctx, request.Tenant, request.PageQuery)
	if err != nil {
		return Response{}, classify(ctx, err)
	}
	result := documents(page.Instances, start)
	result.NextCursor = page.NextCursor
	return result, nil
}

// Related 保留 SDK 的完整集合与身份校验，不提供截断成功结果。
func (s *Service) Related(ctx context.Context, request RelatedRequest) (Response, error) {
	ctx, release, err := s.acquire(ctx)
	if err != nil {
		return Response{}, err
	}
	defer release()
	start := time.Now()
	if request.Query.Limit == 0 {
		request.Query.Limit = 1024
	}
	if err := onemodel.ValidateQuery(request.Tenant, request.Query); err != nil {
		return Response{}, classify(ctx, err)
	}
	if len(request.Roots) < 1 || len(request.Roots) > 1024 || len(request.Relation) == 0 || len(request.Relation) > 256 || (request.Direction != "out" && request.Direction != "in" && request.Direction != "both") {
		return Response{}, &Error{400, "relation, direction and 1..1024 roots are required"}
	}
	roots := make([]onemodel.Instance, 0, len(request.Roots))
	for _, root := range request.Roots {
		q := onemodel.Query{ModelID: root.ModelID, Limit: 1, Where: onemodel.Filter{Field: "model_inst_id", Type: onemodel.InstanceAttributeKeyword, Operator: "eq", Value: root.InstanceID}}
		if err := onemodel.ValidateQuery(request.Tenant, q); err != nil {
			return Response{}, classify(ctx, err)
		}
		if root.InstanceID == "" || len(root.InstanceID) > 1024 {
			return Response{}, &Error{400, "root model_inst_id is required and must not exceed 1024 bytes"}
		}
		roots = append(roots, onemodel.Instance{TenantID: request.Tenant, ModelCode: root.ModelID, InstanceID: root.InstanceID})
	}
	instances, err := s.reader.Related(ctx, request.Tenant, roots, request.Relation, request.Direction, request.Query)
	if err != nil {
		return Response{}, classify(ctx, err)
	}
	return documents(instances, start), nil
}

// Close 释放分页资源；过期快照由 ES 回收，因此关闭可以重复请求。
func (s *Service) Close(ctx context.Context, request CloseRequest) error {
	ctx, release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := s.pages.Close(ctx, request.Tenant, request.Cursor); err != nil {
		return classify(ctx, err)
	}
	return nil
}

func documents(instances []onemodel.Instance, start time.Time) Response {
	result := Response{Items: make([]map[string]any, 0, len(instances)), ElapsedMilliseconds: time.Since(start).Milliseconds()}
	for _, instance := range instances {
		result.Items = append(result.Items, instance.Document())
	}
	return result
}

func classify(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, onemodel.ErrInvalidQuery), errors.Is(err, onemodel.ErrInvalidCursor):
		return &Error{400, err.Error()}
	case errors.Is(err, onemodel.ErrCursorExpired):
		return &Error{410, "查询快照已过期，请重新查询"}
	case errors.Is(err, onemodel.ErrResultLimit):
		return &Error{422, "关联结果超过上限，请收窄条件"}
	case ctx.Err() != nil, errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return &Error{504, "OneModel 查询超时或已取消"}
	default:
		return &Error{502, "OneModel 数据源查询失败，请检查资源配置及服务状态"}
	}
}
