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
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

type SpaceFilter struct {
	ctx      context.Context
	spaceUid string
	space    redis.Space

	mux sync.Mutex
}

// NewSpaceFilter 通过 spaceUid  过滤真实需要使用的 tsDB 实例列表
func NewSpaceFilter(ctx context.Context, spaceUid string) (*SpaceFilter, error) {
	spaceRouter, err := influxdb.GetSpaceRouter("", "")
	if err != nil {
		log.Warnf(ctx, "get space router error, %v", err)
		return nil, err
	}
	space := spaceRouter.Get(ctx, spaceUid)
	if len(space) == 0 {
		msg := fmt.Sprintf("spaceUid: %s is not exists", spaceUid)
		metadata.SetStatus(ctx, metadata.SpaceIsNotExists, msg)
		log.Warnf(ctx, msg)
	}
	return &SpaceFilter{
		ctx:      ctx,
		spaceUid: spaceUid,
		space:    space,
	}, nil
}

func (s *SpaceFilter) DataList(tableID TableID, conditions Conditions, fieldName string) ([]*redis.TsDB, error) {
	if tableID == "" && fieldName == "" {
		return nil, fmt.Errorf("%s, %s", ErrEmptyTableID.Error(), ErrMetricMissing.Error())
	}

	// 判断 tableID 使用几段式
	db, measurement := tableID.Split()
	s.mux.Lock()
	defer s.mux.Unlock()

	isK8s := false
	filterTsDBs := make([]*redis.TsDB, 0)
	// 判断如果 tableID 完整的情况下，则直接取对应的 tsDB
	if db != "" && measurement != "" {
		if v, ok := s.space[string(tableID)]; ok {
			for _, f := range v.Field {
				// fieldName 为空则不对比 field 直接获取 tableid 路由
				if fieldName == "" || f == fieldName {
					filterTsDBs = append(filterTsDBs, v)
					break
				}
			}
		}
	} else if db != "" {
		// 遍历该空间下所有的 space，如果 dataLabel 符合 db 则加入到 tsDB 列表里面
		for _, v := range s.space {
			// 可能会存在重复的 dataLabel
			if db == v.DataLabel {
				for _, f := range v.Field {
					// fieldName 为空则不对比 field 直接获取 tableid 路由
					if fieldName == "" || f == fieldName {
						filterTsDBs = append(filterTsDBs, v)
						break
					}
				}
			}
		}
	} else {
		isK8s = true
		for _, v := range s.space {
			// 如果不指定 tableID 或者 dataLabel，则只获取单指标单表的 tsdb
			if v.IsSplit() {
				for _, f := range v.Field {
					// fieldName 为空则不对比 field 直接获取 tableid 路由
					if fieldName == "" || f == fieldName {
						filterTsDBs = append(filterTsDBs, v)
						break
					}
				}
			}
		}
	}

	// 【针对容器集群的检索优化】将过滤条件与空间 RT 绑定的 bcs_cluster_id 进行对比，如果不符合则过滤掉，符合则返回
	if isK8s {
		resultTsDBs := make([]*redis.TsDB, 0)
		router := influxdb.GetInfluxDBRouter()
		allConditions, err := conditions.AnalysisConditionsV2()
		if err != nil {
			log.Errorf(s.ctx, "failed to analysis conditions for->[%s], nothing will return.", err)
			return nil, err
		}

		for _, tsDB := range filterTsDBs {
			compareResult := true
			router, err := router.GetQueryRouter(tsDB.TableID)
			if err != nil {
				log.Warnf(s.ctx, "failed to get query router for->[%s], skip filter BCS space.", err)
			} else if router.BcsClusterId != "" {
				compareResult, err = allConditions.Compare(ClusterID, router.BcsClusterId)
				if err != nil {
					log.Errorf(s.ctx, "failed to compare conditions for->[%s], nothing will return.", err)
					return nil, err
				}
			}
			if compareResult {
				resultTsDBs = append(resultTsDBs, tsDB)
			}
		}
		filterTsDBs = resultTsDBs
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
		metadata.SetStatus(s.ctx, metadata.SpaceTableIDFieldIsNotExists, msg)
		log.Warnf(s.ctx, msg)
	}

	return filterTsDBs, nil
}

type TsDBOption struct {
	SpaceUid   string
	TableID    TableID
	FieldName  string
	Conditions Conditions
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

	tsDBs, err := spaceFilter.DataList(option.TableID, option.Conditions, option.FieldName)
	if err != nil {
		return nil, err
	}
	return tsDBs, nil
}
