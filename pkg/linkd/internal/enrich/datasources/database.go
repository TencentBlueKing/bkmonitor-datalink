// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package datasources

import (
	"encoding/json"
	"fmt"

	"linkd/internal/domain"
)

func decodeJSONObject(field string, data []byte) (domain.JSONObject, error) {
	var value domain.JSONObject
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("decode %s: %w", field, err)
	}
	value, err := value.Normalize()
	if err != nil {
		return nil, fmt.Errorf("normalize %s: %w", field, err)
	}
	return value, nil
}
