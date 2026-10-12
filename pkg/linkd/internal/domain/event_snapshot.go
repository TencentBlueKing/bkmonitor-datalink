// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain

import (
	"fmt"
	"slices"
)

// WithRelatedAlertIDsFromSnapshot 只复制关联列表；未修改载荷由当前处理链的不可变快照共享。
// 与普通 WithRelatedAlertIDs 不同，返回值和 e 均不得就地修改其来源或已冻结丰富字段。
func (e Event) WithRelatedAlertIDsFromSnapshot(alertIDs []string) (Event, error) {
	alertIDs = normalizeAlertIDs(alertIDs)
	if err := validateRelatedAlertIDs(alertIDs); err != nil {
		return Event{}, err
	}
	if len(e.RelatedAlertIDs) > 0 && !slices.Equal(e.RelatedAlertIDs, alertIDs) {
		return Event{}, fmt.Errorf("event related_alert_ids are already set")
	}
	e.RelatedAlertIDs = alertIDs
	return e, nil
}
