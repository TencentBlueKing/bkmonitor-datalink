// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package relation

import (
	"github.com/spf13/cast"
)

var allowExpandKeys = map[string]struct{}{
	"version":         {},
	"env_type":        {},
	"service_type":    {},
	"service_version": {},
	"env_name":        {},
}

type Item struct {
	ID       string            `json:"id,omitempty"`
	Resource string            `json:"resource,omitempty"`
	Label    map[string]string `json:"label,omitempty"`
}

type Link []Item

// Info
// label 关键维度
// expand 扩展信息 map[{资源类型}]map[{维度 Key}]{维度 value}
// topo 拓扑关联
type Info struct {
	ID       string                       `json:"id,omitempty"`
	Resource string                       `json:"resource,omitempty"`
	Label    map[string]string            `json:"label,omitempty"`
	Expands  map[string]map[string]string `json:"expands,omitempty"`
	Links    []Link                       `json:"links,omitempty"`
}

// ResourceInfo 扩展信息 map[{资源ID}]Info
type ResourceInfo struct {
	Name string           `json:"name,omitempty"`
	Data map[string]*Info `json:"data,omitempty"`
}

func TransformExpands(expands map[string]map[string]any) map[string]map[string]string {
	result := make(map[string]map[string]string, 0)
	for resource, expand := range expands {
		if _, ok := result[resource]; !ok {
			result[resource] = make(map[string]string)
		}

		for k, v := range expand {
			if _, ok := allowExpandKeys[k]; !ok {
				continue
			}

			nv := cast.ToString(v)
			if nv == "" {
				continue
			}

			result[resource][k] = nv
		}
	}

	return result
}

func (e *ResourceInfo) Add(id string, info *Info) {
	if e.Data == nil {
		e.Data = make(map[string]*Info)
	}
	e.Data[id] = info
}

func (e *ResourceInfo) Delete(id string) {
	if e.Data == nil {
		return
	}
	delete(e.Data, id)
}

func (e *ResourceInfo) Reset() {
	e.Data = make(map[string]*Info)
}

func (e *ResourceInfo) Get(id string) *Info {
	return e.Data[id]
}

func (e *ResourceInfo) Range(fn func(info *Info)) {
	if e.Data == nil {
		return
	}

	for _, info := range e.Data {
		fn(info)
	}
}
