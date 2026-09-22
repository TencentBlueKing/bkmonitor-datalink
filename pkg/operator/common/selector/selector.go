// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package selector

import (
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
)

type Op string

const (
	OpDoesNotExist Op = "!"
	OpEquals       Op = "="
	OpDoubleEquals Op = "=="
	OpIn           Op = "in"
	OpNotEquals    Op = "!="
	OpNotIn        Op = "notin"
	OpExists       Op = "exists"
	OpGreaterThan  Op = "gt"
	OpLessThan     Op = "lt"
)

type Requirement struct {
	Key    string
	Op     Op
	Values []string
}

// Selector 用于对资源进行匹配
//
// namespace 为空则表示则命中所有 ns
type Selector struct {
	namespace []string
	selector  labels.Selector
}

func New(namespace []string, required ...Requirement) (*Selector, error) {
	sel := labels.NewSelector()
	for _, r := range required {
		obj, err := labels.NewRequirement(r.Key, selection.Operator(r.Op), r.Values)
		if err != nil {
			return nil, err
		}
		sel.Add(*obj)
	}

	return &Selector{
		namespace: namespace,
		selector:  sel,
	}, nil
}

func (s Selector) matchNamespace(ns string) bool {
	if len(s.namespace) == 0 {
		return true
	}

	for _, namespace := range s.namespace {
		if namespace == ns {
			return true
		}
	}
	return false
}

func (s Selector) Match(namespace string, lbs map[string]string) bool {
	if !s.matchNamespace(namespace) {
		return false
	}

	return s.selector.Matches(labels.Set(lbs))
}
