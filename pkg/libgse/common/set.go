// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package common

// Set is a string set
type Set struct {
	data map[string]bool
}

func NewSet() Set {
	return Set{
		data: make(map[string]bool),
	}
}

func (s *Set) Size() int {
	return len(s.data)
}

func (s *Set) Copy() *Set {
	dst := &Set{
		data: make(map[string]bool),
	}
	for key, value := range s.data {
		dst.data[key] = value
	}
	return dst
}

func (s *Set) Insert(key string) {
	s.data[key] = true
}

func (s *Set) Delete(key string) {
	delete(s.data, key)
}

func (s *Set) Exist(key string) bool {
	_, exist := s.data[key]
	if exist {
		return true
	}
	return false
}

func (s *Set) Keys() map[string]bool {
	return s.data
}

// InterfaceSet
type InterfaceSet struct {
	data map[interface{}]bool
}

func NewInterfaceSet() InterfaceSet {
	return InterfaceSet{
		data: make(map[interface{}]bool),
	}
}

func (s *InterfaceSet) Size() int {
	return len(s.data)
}

func (s *InterfaceSet) Copy() *InterfaceSet {
	dst := &InterfaceSet{
		data: make(map[interface{}]bool),
	}
	for key, value := range s.data {
		dst.data[key] = value
	}
	return dst
}

func (s *InterfaceSet) Insert(key interface{}) {
	s.data[key] = true
}

func (s *InterfaceSet) Delete(key interface{}) {
	delete(s.data, key)
}

func (s *InterfaceSet) Exist(key interface{}) bool {
	_, exist := s.data[key]
	if exist {
		return true
	}
	return false
}

func (s *InterfaceSet) Keys() map[interface{}]bool {
	return s.data
}
