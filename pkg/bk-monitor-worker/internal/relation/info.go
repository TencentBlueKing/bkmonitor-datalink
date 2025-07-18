// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package relation

// Info
// label 关键维度
// expand 扩展信息 map[{资源类型}]map[{维度 Key}]{维度 value}
// topo 拓扑关联
type Info struct {
	ID        string
	Label     map[string]string
	Expand    map[string]map[string]string
	TopoLinks map[string][]map[string]any
}

// ResourceInfo 扩展信息 map[{资源ID}]Info
type ResourceInfo struct {
	name string
	data map[string]*Info
}

func (e *ResourceInfo) Add(id string, info *Info) {
	if e.data == nil {
		e.data = make(map[string]*Info)
	}
	e.data[id] = info
}

func (e *ResourceInfo) Delete(id string) {
	if e.data == nil {
		return
	}
	delete(e.data, id)
}

func (e *ResourceInfo) Reset() {
	e.data = make(map[string]*Info)
}

func (e *ResourceInfo) Get(id string) *Info {
	if e.data == nil {
		return nil
	}

	if v, ok := e.data[id]; ok {
		return v
	}

	return nil
}
