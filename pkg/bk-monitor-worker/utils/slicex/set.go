// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package slicex

import (
	mapset "github.com/deckarep/golang-set"
)

// Set2List convert set to list
func StringSet2List(s mapset.Set) []string {
	t := s.ToSlice()
	var l []string
	for _, v := range t {
		l = append(l, v.(string))
	}
	return l
}

// StringList2Set convert list to set
func StringList2Set(l []string) mapset.Set {
	set := mapset.NewSet()
	for _, i := range l {
		set.Add(i)
	}
	return set
}

// UintSet2List convert set to list
func UintSet2List(s mapset.Set) []uint {
	t := s.ToSlice()
	var l []uint
	for _, v := range t {
		l = append(l, v.(uint))
	}
	return l
}

// UintList2Set convert list to set
func UintList2Set(l []uint) mapset.Set {
	set := mapset.NewSet()
	for _, i := range l {
		set.Add(i)
	}
	return set
}
