// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package collect

import (
	"fmt"

	"linkd/internal/lifecycle/enrich/rules"
)

func projectDisplay(resolution Resolution, subjectName string) string {
	object := subjectName
	if resolution.instanceFound && resolution.instanceErr == nil && validInstance(resolution) {
		if value, ok := rules.FirstField(
			resolution.instance.Fields,
			rules.FieldBKInstDisplayName,
			rules.FieldBKInstName,
		); ok {
			if text := fmt.Sprint(value); text != "" && text != "<nil>" {
				object = text
			}
		}
	}
	return object
}
