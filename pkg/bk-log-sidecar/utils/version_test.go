// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package utils

import (
	"testing"
)

// TestCompareVersion runs test cases for CompareVersion function.
func TestCompareVersion(t *testing.T) {
	tests := []struct {
		name           string
		currentVersion string
		targetVersion  string
		expected       int
	}{
		{"Equal versions", "1.0", "1.0", 0},
		{"Current greater at second part", "1.1", "1.0", 1},
		{"Current less at second part", "1.0", "1.1", -1},
		{"Current longer and greater", "1.0.1", "1.0", 1},
		{"Target longer and greater", "1.0", "1.0.1", -1},
		{"Trailing zeros equal", "1.0.0", "1.0", 0},
		{"All invalid parts", "a.b.c", "0.0.0", 0},
		{"Mixed valid and invalid", "1.a", "1.2", -1},
		{"Empty strings", "", "", 0},
		{"Multiple dots handled", "1..2", "1.0.2", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CompareVersion(tt.currentVersion, tt.targetVersion)
			if result != tt.expected {
				t.Errorf("CompareVersion(%q, %q) = %d; want %d",
					tt.currentVersion, tt.targetVersion, result, tt.expected)
			}
		})
	}
}
