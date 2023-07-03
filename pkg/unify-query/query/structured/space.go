// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"context"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

type SpaceFilter struct {
	ctx      context.Context
	spaceUid string
	space    redis.Space
}

// NewSpaceFilter 通过 spaceUid  过滤真实需要使用的 tsDB 实例列表
func NewSpaceFilter(ctx context.Context, spaceUid string) (*SpaceFilter, error) {
	metric.SpaceRequestCountInc(ctx, metric.SpaceActionGet, spaceUid, metric.StatusReceived)
	spaceRouter, err := influxdb.GetSpaceRouter("", "")
	if err != nil {
		log.Errorf(ctx, "get space router error, %v", err)
		return nil, err
	}
	space := spaceRouter.Get(ctx, spaceUid)
	if len(space) == 0 {
		msg := fmt.Sprintf("spaceUid: %s is not exists", spaceUid)
		metadata.SetStatus(ctx, metadata.SpaceIsNotExists, msg)
		metric.SpaceRequestCountInc(ctx, metric.SpaceActionGet, spaceUid, metric.StatusFailed)
		log.Warnf(ctx, msg)
	} else {
		metric.SpaceRequestCountInc(ctx, metric.SpaceActionGet, spaceUid, metric.StatusSuccess)
	}
	return &SpaceFilter{
		ctx:      ctx,
		spaceUid: spaceUid,
		space:    space,
	}, nil
}

func (s *SpaceFilter) DataList(tableID, fieldName string) ([]*redis.TsDB, error) {
	// 有 tableID 则通过 tableID 过滤
	metric.SpaceTableIDFieldRequestCountInc(
		s.ctx, metric.SpaceActionGet, s.spaceUid, tableID, fieldName, metric.StatusReceived,
	)
	filterTsDBs := make([]*redis.TsDB, 0)
	for tID, tsDB := range s.space {
		// 如果 tableID 不匹配直接跳过
		if tableID != "" {
			if tID != tableID {
				continue
			}
		} else {
			// 如果 tableID 都是空，则只取单指标单表数据
			if !tsDB.IsSplit() {
				continue
			}
		}

		var inField bool
		if fieldName != "" {
			// 判断字段是否在路由表信息里面
			for _, f := range tsDB.Field {
				if fieldName == f {
					inField = true
					break
				}
			}
		} else {
			inField = true
		}

		if inField {
			filterTsDBs = append(filterTsDBs, tsDB)
		}
	}

	if len(s.space) == 0 {
		msg := fmt.Sprintf(
			"spaceUid: %s is not exists",
			s.spaceUid,
		)
		metadata.SetStatus(s.ctx, metadata.SpaceIsNotExists, msg)
		log.Warnf(s.ctx, msg)
	} else if len(filterTsDBs) == 0 {
		msg := fmt.Sprintf(
			"spaceUid: %s and tableID: %s and fieldName: %s is not exists",
			s.spaceUid, tableID, fieldName,
		)
		metric.SpaceTableIDFieldRequestCountInc(
			s.ctx, metric.SpaceActionGet, s.spaceUid, tableID, fieldName, metric.StatusFailed,
		)
		metadata.SetStatus(s.ctx, metadata.SpaceTableIDFieldIsNotExists, msg)
		log.Warnf(s.ctx, msg)
	}
	metric.SpaceTableIDFieldRequestCountInc(
		s.ctx, metric.SpaceActionGet, s.spaceUid, tableID, fieldName, metric.StatusSuccess,
	)
	return filterTsDBs, nil
}

type TsDBOption struct {
	SpaceUid  string
	TableID   string
	FieldName string
}

type TsDBs []*redis.TsDB

func (t TsDBs) StringSlice() []string {
	arr := make([]string, len(t))
	for i, tsDB := range t {
		arr[i] = tsDB.String()
	}
	return arr
}

// GetTsDBList : 通过 spaceUid  约定该空间查询范围
func GetTsDBList(ctx context.Context, option *TsDBOption) (TsDBs, error) {
	spaceFilter, err := NewSpaceFilter(ctx, option.SpaceUid)
	if err != nil {
		return nil, err
	}

	tsDBs, err := spaceFilter.DataList(option.TableID, option.FieldName)
	if err != nil {
		return nil, err
	}
	return tsDBs, nil
}
