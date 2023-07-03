// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils_test

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

type diffObject1 struct {
	Name  string
	Value int
}

type diffObject2 struct {
	ID      int
	Objects []*diffObject1
}

// TestHashIt1 :
func TestHashIt1(t *testing.T) {
	obj1 := diffObject1{
		Name:  "1",
		Value: 1,
	}
	obj2 := diffObject1{
		Name:  "2",
		Value: 2,
	}
	obj3 := diffObject1{
		Name:  "1",
		Value: 1,
	}
	obj4 := diffObject1{
		Name:  "1",
		Value: 2,
	}

	if utils.HashIt(obj1) == utils.HashIt(obj2) {
		t.Errorf("hash error")
	}
	if utils.HashIt(&obj1) == utils.HashIt(&obj2) {
		t.Errorf("hash error")
	}

	if utils.HashIt(obj1) != utils.HashIt(obj3) {
		t.Errorf("hash error")
	}
	if utils.HashIt(&obj1) != utils.HashIt(&obj3) {
		t.Errorf("hash error")
	}

	if utils.HashIt(obj1) == utils.HashIt(obj4) {
		t.Errorf("hash error")
	}
	if utils.HashIt(&obj1) == utils.HashIt(&obj4) {
		t.Errorf("hash error")
	}

	if utils.HashIt(obj2) == utils.HashIt(obj4) {
		t.Errorf("hash error")
	}
	if utils.HashIt(&obj2) == utils.HashIt(&obj4) {
		t.Errorf("hash error")
	}

	if utils.HashIt(&obj1) != utils.HashIt(obj1) {
		t.Errorf("hash error")
	}
}

// TestHashIt2 :
func TestHashIt2(t *testing.T) {
	obj1 := diffObject1{
		Name:  "1",
		Value: 1,
	}
	obj2 := diffObject1{
		Name:  "2",
		Value: 2,
	}
	obj3 := diffObject1{
		Name:  "1",
		Value: 1,
	}

	obj4 := diffObject2{
		ID:      1,
		Objects: []*diffObject1{&obj1},
	}
	obj5 := diffObject2{
		ID:      2,
		Objects: []*diffObject1{&obj1},
	}
	obj6 := diffObject2{
		ID:      1,
		Objects: []*diffObject1{&obj1},
	}
	obj7 := diffObject2{
		ID:      1,
		Objects: []*diffObject1{&obj2},
	}
	obj8 := diffObject2{
		ID:      1,
		Objects: []*diffObject1{&obj3},
	}
	obj9 := diffObject2{
		ID:      1,
		Objects: []*diffObject1{&obj1, &obj2},
	}

	if utils.HashIt(obj4) == utils.HashIt(obj5) {
		t.Errorf("hash error")
	}
	if utils.HashIt(obj4) != utils.HashIt(obj6) {
		t.Errorf("hash error")
	}
	if utils.HashIt(obj4) == utils.HashIt(obj7) {
		t.Errorf("hash error")
	}
	if utils.HashIt(obj4) != utils.HashIt(obj8) {
		t.Errorf("hash error")
	}
	if utils.HashIt(obj4) == utils.HashIt(obj9) {
		t.Errorf("hash error")
	}
}
