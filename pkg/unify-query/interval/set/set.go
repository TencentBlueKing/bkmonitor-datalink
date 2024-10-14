// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package set

type Item interface {
	comparable
}

type Set[T comparable] map[T]struct{}

func New[T comparable](items ...T) Set[T] {
	set := make(Set[T])
	set.Add(items...)
	return set
}

func (s Set[T]) Remove(items ...T) {
	for _, item := range items {
		delete(s, item)
	}
}

func (s Set[T]) Add(items ...T) {
	for _, item := range items {
		s[item] = struct{}{}
	}
}

func (s Set[T]) Existed(item T) bool {
	_, ok := s[item]
	return ok
}

func (s Set[T]) ToArray() []T {
	array := make([]T, 0, len(s))
	for item := range s {
		array = append(array, item)
	}
	return array
}
