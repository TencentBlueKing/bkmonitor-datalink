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
	"strconv"
	"strings"
)

// CompareVersion compares two version strings and returns an integer indicating the relationship between them.
// Returns 1 if currentVersion is greater than targetVersion, -1 if currentVersion is less than targetVersion, and 0 if they are equal.
func CompareVersion(currentVersion, targetVersion string) int {
	currentParts := strings.Split(currentVersion, ".")
	targetParts := strings.Split(targetVersion, ".")

	maxLen := max(len(currentParts), len(targetParts))

	for i := 0; i < maxLen; i++ {
		c := 0
		if i < len(currentParts) {
			if v, err := strconv.Atoi(currentParts[i]); err == nil {
				c = v
			}
		}

		t := 0
		if i < len(targetParts) {
			if v, err := strconv.Atoi(targetParts[i]); err == nil {
				t = v
			}
		}

		if c > t {
			return 1
		} else if c < t {
			return -1
		}
	}

	return 0

}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
