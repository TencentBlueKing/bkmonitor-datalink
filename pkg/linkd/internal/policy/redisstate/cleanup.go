// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redisstate

import (
	"slices"
	"strings"

	"linkd/internal/suppressioncleanup"
)

// cleanupWindows 只接受 Lua 原子返回的完整有界明细，失败不能凭后续空读推断已删除。
func cleanupWindows(raw []any, err error, kind, subject string) ([]suppressioncleanup.Window, error) {
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || raw[0] != "ok" || (len(raw)-1)%3 != 0 || len(raw) > 1+512*3 {
		return nil, ErrState
	}
	result := make([]suppressioncleanup.Window, 0, (len(raw)-1)/3)
	for i := 1; i < len(raw); i += 3 {
		id, idOK := raw[i].(string)
		epoch, epochOK := raw[i+1].(string)
		missing, missingOK := raw[i+2].(int64)
		w := suppressioncleanup.Window{ID: id, Epoch: epoch, Missing: missing == 1}
		if !idOK || !epochOK || !missingOK || (missing != 0 && missing != 1) || w.Validate(kind) != nil || (kind == "clip" && !strings.HasPrefix(id, subject+":")) {
			return nil, ErrState
		}
		result = append(result, w)
	}
	slices.SortFunc(result, func(a, b suppressioncleanup.Window) int { return strings.Compare(a.ID, b.ID) })
	for i := 1; i < len(result); i++ {
		if result[i].ID == result[i-1].ID {
			return nil, ErrState
		}
	}
	return result, nil
}
