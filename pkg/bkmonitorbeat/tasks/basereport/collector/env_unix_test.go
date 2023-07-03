// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build linux
// +build linux

package collector

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRegexValue(t *testing.T) {
	cases := []struct {
		name      string
		content   []byte
		expected  int
		expectErr bool
	}{
		{
			name:     "case  1",
			content:  []byte("testing  42"),
			expected: 42,
		},
		{
			name:      "case  2",
			content:   []byte("testing"),
			expectErr: true,
		},
		{
			name:      "case  3",
			content:   []byte("testing  4A"),
			expectErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := regexValues(tc.name, tc.content)
			assert.True(t, tc.expectErr)
			assert.NoError(t, err)
			assert.Equal(t, actual, tc.expected)
		})
	}
}

func regexValues(name string, content []byte) (int, error) {
	expr := name + "\\s[0-9]"
	reg, err := regexp.Compile(expr)
	if err != nil {
		return 0, err
	}

	line := reg.Find(content)
	if line == nil {
		return 0, nil
	}

	value := strings.Split(string(line), "  ")

	return strconv.Atoi(value[len(value)-1])
}
