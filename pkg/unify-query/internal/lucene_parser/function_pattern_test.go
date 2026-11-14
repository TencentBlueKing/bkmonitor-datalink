// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lucene_parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractBoost(t *testing.T) {
	for _, c := range []struct{ in, base, boost string }{
		{"foo^2", "foo", "2"},
		{"foo^1.5", "foo", "1.5"},
		{"a^b^3", "a^b", "3"},
		{"foo", "foo", ""},
		{"foo^", "foo^", ""},
		{"foo^x", "foo^x", ""},
		{"^2", "^2", ""},
	} {
		base, boost := extractBoost(c.in)
		assert.Equal(t, [2]string{c.base, c.boost}, [2]string{base, boost}, c.in)
	}
}

func TestExtractRange(t *testing.T) {
	node, matched := extractRange("[1TO5]", "2")
	require.True(t, matched)
	assert.Equal(t, &RangeNode{
		Start: &StringNode{Value: "1"}, End: &StringNode{Value: "5"}, IsIncludeStart: true, IsIncludeEnd: true, Boost: "2",
	}, node)

	node, matched = extractRange(`{*TO"10"}`, "")
	require.True(t, matched)
	assert.Equal(t, &RangeNode{End: &StringNode{Value: "10"}}, node)

	for _, in := range []string{"1TO5", "[1 5]", "[TO]", "(1TO5)", ""} {
		node, matched := extractRange(in, "")
		assert.False(t, matched, in)
		assert.Nil(t, node, in)
	}
}

func TestExtractRegexp(t *testing.T) {
	node, matched := extractRegexp("/ab.*/", "2")
	require.True(t, matched)
	assert.Equal(t, &RegexpNode{Value: "ab.*", Boost: "2"}, node)

	for _, in := range []string{"//", "ab", "/ab", "ab/", ""} {
		node, matched := extractRegexp(in, "")
		assert.False(t, matched, in)
		assert.Nil(t, node, in)
	}
}
