// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package basetarget

import (
	"fmt"

	"linkd/internal/lifecycle/enrich/rules"
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
