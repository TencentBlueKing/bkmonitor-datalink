// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package detect

import (
	"context"
	"errors"
	"sort"

	inputv2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/input/adapter/v2"
)

type selectedRecord struct {
	ordinal                 uint32
	view                    inputv2.RecordView
	dimensionIdentityDigest string
	sourceTime              int64
	recordID                string
}

type seriesGroup struct {
	dimensionIdentityDigest string
	records                 []selectedRecord
}

func collectSelectedRecords(ctx context.Context, view inputv2.PlanView) ([]selectedRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if view.PlanID() == "" {
		return nil, &InternalError{Operation: "collect selected records", Err: errors.New("plan ID is empty")}
	}
	records := make([]selectedRecord, 0, view.SelectedCount())
	err := view.ForEachSelectedSlot(func(ordinal uint32, record inputv2.RecordView, valid bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !valid {
			return nil
		}
		records = append(records, selectedRecord{
			ordinal:                 ordinal,
			view:                    record,
			dimensionIdentityDigest: record.DimensionIdentityDigest(),
			sourceTime:              record.SourceTime(),
			recordID:                record.RecordID(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

func groupSelectedRecords(records []selectedRecord) []seriesGroup {
	if len(records) == 0 {
		return nil
	}
	groups := make([]seriesGroup, 0)
	byDigest := make(map[string]int)
	for _, record := range records {
		index, ok := byDigest[record.dimensionIdentityDigest]
		if !ok {
			index = len(groups)
			byDigest[record.dimensionIdentityDigest] = index
			groups = append(groups, seriesGroup{dimensionIdentityDigest: record.dimensionIdentityDigest})
		}
		groups[index].records = append(groups[index].records, record)
	}
	sort.Slice(groups, func(left, right int) bool {
		return groups[left].dimensionIdentityDigest < groups[right].dimensionIdentityDigest
	})
	for index := range groups {
		if recordsOrdered(groups[index].records) {
			continue
		}
		sort.SliceStable(groups[index].records, func(left, right int) bool {
			return recordBefore(groups[index].records[left], groups[index].records[right])
		})
	}
	return groups
}

func recordsOrdered(records []selectedRecord) bool {
	for index := 1; index < len(records); index++ {
		if recordBefore(records[index], records[index-1]) {
			return false
		}
	}
	return true
}

func recordBefore(left, right selectedRecord) bool {
	if left.sourceTime != right.sourceTime {
		return left.sourceTime < right.sourceTime
	}
	return left.recordID < right.recordID
}
