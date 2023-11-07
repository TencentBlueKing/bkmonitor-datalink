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

package corefile

import (
	"testing"
)

func TestTranslate(t *testing.T) {
	testCases := []struct {
		text     string
		expected string
	}{
		{"", ""},
		{"1", "hangup"},
		{"2", "interrupt"},
		{"9999", ""},
		{"abc", "abc"},
	}

	translator := &SignalTranslator{}

	for _, testCase := range testCases {
		result := translator.Translate(testCase.text)
		if result != testCase.expected {
			t.Errorf("Expected %s, but got %s", testCase.expected, result)
		}
	}
}

func TestExecutablePathTranslator_Translate(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"../path!to!file.ext", "../path/to/file.ext"},
		{"folder!/file!/name.ext", "folder/file/name.ext"},
		{"root!!file.txt", "root//file.txt"},
		{"no!exclamation!mark!in!path", "no!exclamation!mark!in!path"},
		{"!at!the!beginning", "/at/the/beginning"},
		{"at!the!end!", "at/the/end/"},
	}

	translator := &ExecutablePathTranslator{}

	for _, test := range tests {
		result := translator.Translate(test.path)
		if result != test.expected {
			t.Errorf("Translate(%q) = %q, want %q", test.path, result, test.expected)
		}
	}
}
