// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package querystring

import (
	"fmt"
)

var (
	DefaultKey = "log" // 默认字段名，与 Doris 的 DefaultKey 一致
)

func LabelMap(query string) (map[string][]string, error) {
	expr, err := Parse(query)
	if err != nil {
		return nil, err
	}

	labelMap := make(map[string][]string)
	if err := parseExprToKeyValue(query, expr, labelMap); err != nil {
		return nil, err
	}

	return labelMap, nil
}

func parseExprToKeyValue(query string, expr Expr, kv map[string][]string) error {
	if expr == nil {
		return nil
	}

	switch e := expr.(type) {
	case *NotExpr:
		if err := parseExprToKeyValue(query, e.Expr, kv); err != nil {
			return err
		}
	case *OrExpr:
		if err := parseExprToKeyValue(query, e.Left, kv); err != nil {
			return err
		}
		if err := parseExprToKeyValue(query, e.Right, kv); err != nil {
			return err
		}
	case *AndExpr:
		if err := parseExprToKeyValue(query, e.Left, kv); err != nil {
			return err
		}
		if err := parseExprToKeyValue(query, e.Right, kv); err != nil {
			return err
		}
	case *WildcardExpr:
		field := e.Field
		if field == "" {
			field = DefaultKey
		}
		if err := addValueToMap(kv, field, e.Value); err != nil {
			return fmt.Errorf("failed to add value to map: %w", err)
		}
	case *MatchExpr:
		field := e.Field
		if field == "" {
			field = DefaultKey
		}
		if err := addValueToMap(kv, field, e.Value); err != nil {
			return fmt.Errorf("failed to add value to map: %w", err)
		}
	default:
		return nil
	}
	return nil
}

func addValueToMap(kv map[string][]string, field, value string) error {
	if kv == nil {
		return fmt.Errorf("kv map is nil")
	}
	if value == "" {
		return fmt.Errorf("value cannot be empty")
	}

	for _, v := range kv[field] {
		if v == value {
			return nil
		}
	}

	kv[field] = append(kv[field], value)
	return nil
}
