// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package hashring

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewHashRing(t *testing.T) {
	nodes := map[string]int{"node1": 1, "node2": 1, "node3": 1}
	hr := NewHashRing(nodes, 1<<16)
	items := map[string]string{
		"1": "node2",
		"2": "node1",
		"3": "node2",
		"4": "node3",
		"5": "node2",
		"6": "node1",
	}
	for k, v := range items {
		assert.Equal(t, hr.GetNode(k), v)
	}

	nodes2 := map[string]int{"node1": 1, "node2": 3, "node3": 5}
	hr2 := NewHashRing(nodes2, 1<<16)
	items2 := map[string]string{
		"1":  "node1",
		"2":  "node2",
		"3":  "node2",
		"4":  "node2",
		"5":  "node2",
		"6":  "node3",
		"7":  "node2",
		"8":  "node3",
		"9":  "node3",
		"10": "node1",
	}
	for k, v := range items2 {
		assert.Equal(t, hr2.GetNode(k), v)
	}
}
