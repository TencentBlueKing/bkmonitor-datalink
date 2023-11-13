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
	"testing"

	mapset "github.com/deckarep/golang-set"
	"github.com/stretchr/testify/assert"
)

// TestStringSet2List
func TestStringSet2List(t *testing.T) {
	m := StringList2Set([]string{"a", "b", "c"})
	mStr := StringSet2List(m)
	assert.Equal(t, len(mStr), 3)
}

// TestStringList2Set
func TestStringList2Set(t *testing.T) {
	m := StringList2Set([]string{"a", "b", "a"})
	expected := mapset.NewSet()
	for _, v := range []string{"a", "b"} {
		expected.Add(v)
	}
	assert.Equal(t, expected, m)
}
