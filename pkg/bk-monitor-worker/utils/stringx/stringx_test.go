// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package stringx

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestString2byte(t *testing.T) {
	src := "test string"
	dest := []byte(src)

	assert.Equal(t, String2byte(src), dest)
}

func TestStringInSlice(t *testing.T) {
	assert.True(t, StringInSlice("a", []string{"a", "b"}))
	assert.False(t, StringInSlice("a", []string{"b", "c"}))
}

func TestSplitString(t *testing.T) {
	assert.Equal(t, []string{"a", "b"}, SplitString("a,b"))
	assert.Equal(t, []string{"a", "b"}, SplitString("a b"))
	assert.Equal(t, []string{"a", "b"}, SplitString("a;b"))

	assert.Equal(t, []string{"a.b"}, SplitString("a.b"))
}

func TestSplitStringByDot(t *testing.T) {
	assert.Equal(t, []string{"a", "b"}, SplitStringByDot("a.b"))

	assert.Equal(t, []string{"a,b"}, SplitStringByDot("a,b"))
}
