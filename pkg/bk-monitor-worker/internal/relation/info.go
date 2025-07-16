// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package relation

// ExpandInfo 扩展信息 map[{维度 Key}]{维度 value}
type ExpandInfo map[string]string

// ExpandInfos 扩展信息 map[{资源ID}]map[{资源类型}]ap[{维度 Key}]{维度 value}
type ExpandInfos struct {
	data map[string]ExpandInfo
}

func (e *ExpandInfos) Add(id string, data map[string]string) {
	if e.data == nil {
		e.data = make(map[string]ExpandInfo)
	}
	e.data[id] = data
}

func (e *ExpandInfos) Delete(id string) {
	if e.data == nil {
		return
	}
	delete(e.data, id)
}

func (e *ExpandInfos) Reset() {
	e.data = make(map[string]ExpandInfo)
}

func (e *ExpandInfos) Get(id string) map[string]string {
	if e.data == nil {
		return nil
	}

	if v, ok := e.data[id]; ok {
		return v
	}

	return nil
}

// ResourceExpandInfos 扩展信息 map[{资源类型}]map[{资源ID}]map[{维度 Key}]{维度 value}
type ResourceExpandInfos struct {
	data map[string]*ExpandInfos
}

func NewResourceExpandInfos(keys ...string) *ResourceExpandInfos {
	r := &ResourceExpandInfos{
		data: make(map[string]*ExpandInfos),
	}

	for _, key := range keys {
		r.data[key] = &ExpandInfos{
			data: make(map[string]ExpandInfo),
		}
	}
	return r
}

func (e *ResourceExpandInfos) Reset() {
	if e.data == nil {
		return
	}

	for _, v := range e.data {
		v.Reset()
	}
}

func (e *ResourceExpandInfos) Get(name string) *ExpandInfos {
	if e.data == nil {
		return nil
	}

	if v, ok := e.data[name]; ok {
		return v
	}

	return nil
}

func (e *ResourceExpandInfos) Delete(name string, ids ...string) {
	if e.data == nil {
		return
	}

	if ei, ok := e.data[name]; ok {
		for _, id := range ids {
			ei.Delete(id)
		}
	}
}
