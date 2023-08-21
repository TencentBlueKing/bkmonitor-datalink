// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
)

func CloneMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}

	dst := make(map[string]string)
	for key, value := range m {
		dst[key] = value
	}
	return dst
}

func MergeMaps(ms ...map[string]string) map[string]string {
	dst := make(map[string]string)
	for _, m := range ms {
		for k, v := range m {
			dst[k] = v
		}
	}

	return dst
}

func MergeReplaceMaps(ms ...map[string]string) map[string]string {
	dst := make(map[string]string)
	for _, m := range ms {
		for k, v := range m {
			newKey := strings.ReplaceAll(k, ".", "_")
			dst[newKey] = v
		}
	}

	return dst
}

func MergeReplaceAttributeMaps(attrs ...pcommon.Map) map[string]string {
	dst := make(map[string]string)
	for _, attr := range attrs {
		attr.Range(func(k string, v pcommon.Value) bool {
			newKey := strings.ReplaceAll(k, ".", "_")
			dst[newKey] = v.AsString()
			return true
		})
	}
	return dst
}
