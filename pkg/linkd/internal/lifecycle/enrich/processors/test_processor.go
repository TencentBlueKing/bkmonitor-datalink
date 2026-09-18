// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/rules"
)

// Test 通过注入的数据源端口模拟调用负载，成功后返回配置字段。
// 配置与每次返回值独立持有数据，多个租户/告警并发执行时不会共享可变结果。
type Test struct {
	fields     domain.JSONObject
	datasource enrich.TestSourceConfig
}

// NewTest 冻结 fields 和 datasource 参数；配置最多 64 KiB。
// fields 写入当前处理器的 value，不修改 Alert 的身份、状态或其他处理器输出。
func NewTest(config map[string]any) (Test, error) {
	data, err := json.Marshal(config)
	if err != nil {
		return Test{}, fmt.Errorf("test processor config must be JSON compatible")
	}
	if len(data) > 64<<10 {
		return Test{}, fmt.Errorf("test processor config must not exceed 65536 bytes")
	}
	var decoded struct {
		Fields     domain.JSONObject       `json:"fields"`
		Datasource enrich.TestSourceConfig `json:"datasource"`
	}
	decoded.Datasource = enrich.DefaultTestSourceConfig()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return Test{}, fmt.Errorf("test processor config requires valid fields and datasource objects; unknown fields are not allowed")
	}
	if err := decoded.Datasource.Validate(); err != nil {
		return Test{}, err
	}
	fields, err := decoded.Fields.Normalize()
	if err != nil {
		return Test{}, fmt.Errorf("test processor fields must be a valid JSON object")
	}
	return Test{fields: fields, datasource: decoded.Datasource}, nil
}

// Name 返回稳定的处理器名，用于丰富结果和指标。
func (Test) Name() string { return rules.TestProcessor }

// Match 对未取消的所有输入启用测试负载，不依赖租户或业务字段。
func (Test) Match(ctx context.Context, _ *enrich.Scope) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return true, nil
}

// Process 顺序调用配置次数的数据源，首次错误立即返回，不在处理器内重试。
// Chain 将调用错误归为 failed；父取消则终止整次丰富，成功才返回固定字段。
func (p Test) Process(ctx context.Context, scope *enrich.Scope) (enrich.ProcessorResult, error) {
	if err := ctx.Err(); err != nil {
		return enrich.ProcessorResult{}, err
	}
	if scope == nil {
		return enrich.ProcessorResult{}, fmt.Errorf("test processor requires enrich scope")
	}
	for index := 0; index < p.datasource.Calls; index++ {
		if err := scope.CallTestSource(ctx, p.datasource, index); err != nil {
			return enrich.ProcessorResult{}, err
		}
	}
	return enrich.ProcessorResult{Status: domain.EnrichStatusSucceeded, Value: p.fields.Clone()}, nil
}
