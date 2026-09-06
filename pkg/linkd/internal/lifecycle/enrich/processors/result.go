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
	"encoding/json"
	"fmt"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich"
)

func object(values map[string]any) (domain.JSONObject, error) {
	result := make(domain.JSONObject, len(values))
	for key, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("marshal processor field %q: %w", key, err)
		}
		result[key] = encoded
	}
	return result.Normalize()
}

func failedDependency(dependency string) enrich.ProcessorResult {
	return enrich.ProcessorResult{
		Status:      domain.EnrichStatusFailed,
		Value:       domain.JSONObject{},
		Diagnostics: []enrich.Diagnostic{{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: dependency}},
	}
}
