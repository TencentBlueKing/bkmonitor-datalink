// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package basetarget

import (
	"fmt"

	"linkd/internal/enrich/rules"
)

func projectDisplay(resolution resolution, subjectName string) string {
	if resolution.branch == rules.BaseTargetBasic || resolution.instanceErr != nil || !resolution.instanceFound || !validInstance(resolution) {
		return subjectName
	}
	fields := mergeInstanceFields(resolution.instance)
	if value, ok := rules.FirstField(fields,
		rules.FieldBKInstDisplayName, rules.FieldBKInstName, "display_name", "bk_host_name", rules.FieldBKHostInnerIP,
	); ok {
		if text := fmt.Sprint(value); text != "" && text != "<nil>" {
			return text
		}
	}
	return subjectName
}
