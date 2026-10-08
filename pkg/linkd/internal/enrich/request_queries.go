// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package enrich

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"linkd/internal/enrich/custom"
	"linkd/internal/jsonpath"
	"linkd/internal/onemodel"
)

// queryCacheKey 用类型化 JSON 区分查询参数，只在单 Event 内缓存；租户仍显式参与键。
func queryCacheKey(kind string, args ...any) (string, error) {
	encoded, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	if len(encoded) > 1<<20 {
		return "", fmt.Errorf("enrich query parameters exceed budget")
	}
	digest := sha256.Sum256(encoded)
	return "query:" + kind + ":" + hex.EncodeToString(digest[:]), nil
}

type ruleInstanceReader struct {
	scope *Scope
	next  onemodel.Reader
}

func (r ruleInstanceReader) Search(ctx context.Context, tenant string, q onemodel.Query) ([]onemodel.Instance, error) {
	key, err := queryCacheKey("search", tenant, q)
	if err != nil {
		return nil, err
	}
	value, err := r.scope.Scenario(key, func() (any, error) { return r.next.Search(ctx, tenant, q) })
	if err != nil {
		return nil, err
	}
	return cloneQueryInstances(value.([]onemodel.Instance)), nil
}

func (r ruleInstanceReader) Related(ctx context.Context, tenant string, from []onemodel.Instance, relation, direction string, q onemodel.Query) ([]onemodel.Instance, error) {
	key, err := queryCacheKey("related", tenant, from, relation, direction, q)
	if err != nil {
		return nil, err
	}
	value, err := r.scope.Scenario(key, func() (any, error) { return r.next.Related(ctx, tenant, from, relation, direction, q) })
	if err != nil {
		return nil, err
	}
	return cloneQueryInstances(value.([]onemodel.Instance)), nil
}

func cloneQueryInstances(values []onemodel.Instance) []onemodel.Instance {
	result := append([]onemodel.Instance(nil), values...)
	for i := range result {
		if result[i].Fields != nil {
			result[i].Fields = jsonpath.Clone(result[i].Fields).(map[string]any)
		}
		if result[i].Attributes != nil {
			result[i].Attributes = jsonpath.Clone(result[i].Attributes).(map[string]any)
		}
	}
	return result
}

type ruleDisplayReader struct {
	scope *Scope
	next  custom.DisplayReader
}

func (r ruleDisplayReader) Format(ctx context.Context, tenant, model, field string, input any) (any, error) {
	key, err := queryCacheKey("format", tenant, model, field, input)
	if err != nil {
		return nil, err
	}
	value, err := r.scope.Scenario(key, func() (any, error) { return r.next.Format(ctx, tenant, model, field, input) })
	if err != nil {
		return nil, err
	}
	return jsonpath.Clone(value), nil
}

// FindCMDBTopology 保留 SDK 的完整拓扑能力；查询缓存仍局限于本次事件和租户。
func (r ruleInstanceReader) FindCMDBTopology(ctx context.Context, tenant, host string) (onemodel.ResourceTopology, bool, error) {
	reader, ok := r.next.(interface {
		FindCMDBTopology(context.Context, string, string) (onemodel.ResourceTopology, bool, error)
	})
	if !ok {
		return onemodel.ResourceTopology{}, false, fmt.Errorf("CMDB topology datasource unavailable")
	}
	return reader.FindCMDBTopology(ctx, tenant, host)
}
