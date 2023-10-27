// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package common

import (
	"testing"
)

func Test_Set(t *testing.T) {
	s := NewSet()

	// empty
	n := s.Size()
	if n != 0 {
		t.Error("set is not empty")
		t.Fail()
	}

	s.Insert("abc1")
	s.Insert("abc2")
	s.Insert("abc1")

	n = s.Size()
	if n != 2 {
		t.Error("set size is not 2")
		t.Fail()
	}

	s2 := s.Copy()
	if !s2.Exist("abc2") {
		t.Error("can not find abc2")
		t.Fail()
	}

	s2.Delete("abc2")

	if s2.Exist("abc2") {
		t.Error("should not exist abc2")
		t.Fail()
	}

	if s.Size() != 2 {
		t.Error("copy not work")
		t.Fail()
	}

	for key := range s.Keys() {
		t.Log("key:", key)
	}
}

func Test_IntSet(t *testing.T) {
	s := NewInterfaceSet()

	// empty
	n := s.Size()
	if n != 0 {
		t.Error("set is not empty")
		t.Fail()
	}

	s.Insert(111)
	s.Insert(222)
	s.Insert(111)

	n = s.Size()
	if n != 2 {
		t.Error("set size is not 2")
		t.Fail()
	}

	s2 := s.Copy()
	if !s2.Exist(222) {
		t.Error("can not find 222")
		t.Fail()
	}

	s2.Delete(222)

	if s2.Exist(222) {
		t.Error("should not exist 222")
		t.Fail()
	}

	if s.Size() != 2 {
		t.Error("copy not work")
		t.Fail()
	}

	for key := range s.Keys() {
		t.Log("key:", key)
	}
}
