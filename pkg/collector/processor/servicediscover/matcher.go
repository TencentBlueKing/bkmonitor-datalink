// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package servicediscover

import (
	"regexp"
	"strings"

	"go.opentelemetry.io/collector/pdata/ptrace"
)

type ReplaceType string

const (
	ReplaceMissing = "missing" // attributes 以数据本身优先 如若数据中不存在该 Key 则覆盖
	ReplaceForce   = "force"   // attributes 强制替换
)

func NewMatcher() Matcher {
	return Matcher{}
}

type Matcher struct{}

func (Matcher) Match(span ptrace.Span, mappings map[string]string, replaceType string) {
	for k, v := range mappings {
		switch k {
		case "span_name":
			span.SetName(v)
		default:
			switch replaceType {
			case ReplaceForce:
				span.Attributes().UpsertString(k, v)
			default:
				if _, ok := span.Attributes().Get(k); !ok {
					span.Attributes().UpsertString(k, v)
				}
			}
		}
	}
}

type Op string

// match_op 支持：reg/eq/nq/startswith/nstartswith/endswith/nendswith/contains/ncontains
const (
	OpReg         Op = "reg"
	OpEq          Op = "eq"
	OpNq          Op = "nq"
	OpStartsWith  Op = "startswith"
	OpNStartsWith Op = "nstartswith"
	OpEndsWith    Op = "endswith"
	OpNEndsWith   Op = "nendswith"
	OpContains    Op = "contains"
	OpNContains   Op = "ncontains"
)

func OperatorMatch(input, expected string, op string) bool {
	switch Op(op) {
	case OpReg:
		matched, err := regexp.MatchString(expected, input)
		if err != nil {
			return false
		}
		return matched
	case OpEq:
		return input == expected
	case OpNq:
		return input != expected
	case OpStartsWith:
		return strings.HasPrefix(input, expected)
	case OpNStartsWith:
		return !strings.HasPrefix(input, expected)
	case OpEndsWith:
		return strings.HasSuffix(input, expected)
	case OpNEndsWith:
		return !strings.HasSuffix(input, expected)
	case OpContains:
		return strings.Contains(input, expected)
	case OpNContains:
		return !strings.Contains(input, expected)
	}
	return false
}
