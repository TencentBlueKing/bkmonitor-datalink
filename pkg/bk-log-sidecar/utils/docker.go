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

import "regexp"

const (
	DockerVersionApiError     = "Maximum supported API version is"
	DockerVersionErrorPattern = "Maximum supported API version is (\\d+\\.\\d+)"
)

// ExtractDockerApiVersion extract docker api version
func ExtractDockerApiVersion(errParam error) (bool, string) {
	re, err := regexp.Compile(DockerVersionErrorPattern)
	if err != nil {
		return false, ""
	}
	result := re.FindAllStringSubmatch(errParam.Error(), -1)
	if len(result) > 0 {
		if len(result[0]) > 1 {
			return true, result[0][1]
		}
	}
	return false, ""
}
