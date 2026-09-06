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
	"encoding/json"
	"fmt"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich/models"
)

// StrategyContext 只持有 strategy Processor 的输出。
type StrategyContext struct{ Values models.StrategyValues }

func (c *StrategyContext) Set(values models.StrategyValues) { c.Values = values }
func (c *StrategyContext) JSONObject() (domain.JSONObject, error) {
	return valuesObject(c.Values)
}

// ResourceContext 只持有 resource Processor 的输出。
type ResourceContext struct{ Values models.ResourceValues }

func (c *ResourceContext) Set(values models.ResourceValues) { c.Values = values }
func (c *ResourceContext) JSONObject() (domain.JSONObject, error) {
	return valuesObject(c.Values)
}

// DisplayContext 只持有 display Processor 的输出。
type DisplayContext struct{ Values models.DisplayValues }

func (c *DisplayContext) Set(values models.DisplayValues) { c.Values = values }
func (c *DisplayContext) JSONObject() (domain.JSONObject, error) {
	return valuesObject(c.Values)
}

// MetricContext 只持有 metric Processor 的输出。
type MetricContext struct{ Values models.MetricValues }

func (c *MetricContext) Set(values models.MetricValues) { c.Values = values }
func (c *MetricContext) JSONObject() (domain.JSONObject, error) {
	return valuesObject(c.Values)
}

// LogContext 只持有 log Processor 的输出。
type LogContext struct{ Values models.LogValues }

func (c *LogContext) Set(values models.LogValues) { c.Values = values }
func (c *LogContext) JSONObject() (domain.JSONObject, error) {
	return valuesObject(c.Values)
}

// APMContext 只持有 apm Processor 的输出。
type APMContext struct{ Values models.APMValues }

func (c *APMContext) Set(values models.APMValues) { c.Values = values }
func (c *APMContext) JSONObject() (domain.JSONObject, error) {
	return valuesObject(c.Values)
}

// K8sContext 只持有 k8s Processor 的输出。
type K8sContext struct{ Values models.K8sValues }

func (c *K8sContext) Set(values models.K8sValues) { c.Values = values }
func (c *K8sContext) JSONObject() (domain.JSONObject, error) {
	return valuesObject(c.Values)
}

// SourceContext 只持有 source Processor 的输出。
type SourceContext struct{ Values models.SourceValues }

func (c *SourceContext) Set(values models.SourceValues) { c.Values = values }
func (c *SourceContext) JSONObject() (domain.JSONObject, error) {
	return valuesObject(c.Values)
}

// EnrichContext 保存单次 Enrich 调用中各输出分组的类型化 Values。
// 每个 Context 只包含自身输出字段；零值字段同样进入 JSON 并交由下游解释。
type EnrichContext struct {
	Strategy StrategyContext
	Resource ResourceContext
	Display  DisplayContext
	Metric   MetricContext
	Log      LogContext
	APM      APMContext
	K8s      K8sContext
	Source   SourceContext
}

func valuesObject[T any](values T) (domain.JSONObject, error) {
	data, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("marshal enrich context values: %w", err)
	}
	var object domain.JSONObject
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, fmt.Errorf("decode enrich context values: %w", err)
	}
	return object.Normalize()
}
