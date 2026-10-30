// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.
package v1beta1

func selectLegacyPathResult(results []pathQueryResult) pathQueryResult {
	var selected pathQueryResult
	for _, result := range results {
		selected = result
		if len(result.ts) > 0 {
			break
		}
	}
	return selected
}
