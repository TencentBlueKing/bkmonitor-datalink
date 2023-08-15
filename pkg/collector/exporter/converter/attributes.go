// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package converter

import (
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
)

func MergeAttributeMaps(dst pcommon.Map, attrs ...pcommon.Map) map[string]interface{} {
	for _, attr := range attrs {
		attr.Range(func(k string, v pcommon.Value) bool {
			dst.Upsert(k, v)
			return true
		})
	}
	return dst.AsRaw()
}

func MergeMaps(dst map[string]interface{}, attrs ...map[string]interface{}) map[string]interface{} {
	for _, attr := range attrs {
		for k, v := range attr {
			dst[k] = v
		}
	}
	return dst
}

func MergeReplaceAttributeMaps(dst pcommon.Map, attrs ...pcommon.Map) map[string]interface{} {
	return ReplaceDotToUnderline(MergeAttributeMaps(dst, attrs...))
}

func MergeReplaceMaps(dst map[string]interface{}, attrs ...map[string]interface{}) map[string]interface{} {
	return ReplaceDotToUnderline(MergeMaps(dst, attrs...))
}

func ReplaceDotToUnderline(raw map[string]interface{}) map[string]interface{} {
	ret := make(map[string]interface{}, len(raw))
	for k, v := range raw {
		newKey := strings.ReplaceAll(k, ".", "_")
		ret[newKey] = v
	}
	return ret
}
