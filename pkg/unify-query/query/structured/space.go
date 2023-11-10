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
	"regexp"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	routerInfluxdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

type SpaceFilter struct {
	ctx      context.Context
	spaceUid string
	router   *influxdb.SpaceTsDbRouter
	space    routerInfluxdb.Space
}

// NewSpaceFilter 通过 spaceUid  过滤真实需要使用的 tsDB 实例列表
func NewSpaceFilter(ctx context.Context, spaceUid string) (*SpaceFilter, error) {
	router, err := influxdb.GetSpaceTsDbRouter()
	if err != nil {
		return nil, err
	}
	space := router.GetSpace(ctx, spaceUid)
	if space == nil {
		msg := fmt.Sprintf("spaceUid: %s is not exists", spaceUid)
		metadata.SetStatus(ctx, metadata.SpaceIsNotExists, msg)
		log.Warnf(ctx, msg)
	}
	return &SpaceFilter{
		ctx:      ctx,
		spaceUid: spaceUid,
		router:   router,
		space:    space,
	}, nil
}

func (s *SpaceFilter) NewTsDBs(spaceTable *routerInfluxdb.SpaceResultTable, fieldNameExp *regexp.Regexp,
	fieldName, tableID string, isK8s, isK8sFeatureFlag bool) []*query.TsDBV2 {
	rtDetail := s.router.GetResultTable(s.ctx, tableID)
	if rtDetail == nil {
		return nil
	}

	// 当传入有效的 measurementType 字段时，需要进行类型过滤
	if isK8s {
		if rtDetail.MeasurementType != redis.BkSplitMeasurement {
			return nil
		}

		if isK8sFeatureFlag {
			// 如果是只查询 k8s 的 rt，则需要判断 bcsClusterID 字段不为空
			if rtDetail.BcsClusterID == "" {
				return nil
			}
		}
	}

	filters := make([]query.Filter, 0, len(spaceTable.Filters))
	for _, f := range spaceTable.Filters {
		filters = append(filters, f)
	}

	tsDBs := make([]*query.TsDBV2, 0)
	defaultMetricNames := make([]string, 0)
	// 原 Space(Type、BKDataID) 字段去掉，SegmentedEnable 设置了默认值 false
	// 原 Proxy(RetentionPolicies、BKBizID、DataID）直接去掉
	defaultTsDB := query.TsDBV2{
		TableID:         tableID,
		Field:           rtDetail.Fields,
		MeasurementType: rtDetail.MeasurementType,
		Filters:         filters,
		SegmentedEnable: false,
		DataLabel:       rtDetail.DataLabel,
		StorageID:       strconv.Itoa(int(rtDetail.StorageId)),
		ClusterName:     rtDetail.ClusterName,
		TagsKey:         rtDetail.TagsKey,
		DB:              rtDetail.DB,
		Measurement:     rtDetail.Measurement,
		VmRt:            rtDetail.VmRt,
		MetricName:      fieldName,
	}
	// 字段为空时，需要返回结果表的信息，表示无需过滤字段过滤
	if fieldName == "" {
		tsDBs = append(tsDBs, &defaultTsDB)
		return tsDBs
	}
	// 字段不为空时，需要判断是否有匹配字段，返回解析后的结果
	metricNames := make([]string, 0)
	for _, f := range rtDetail.Fields {
		if f == fieldName || (fieldNameExp != nil && fieldNameExp.Match([]byte(f))) {
			metricNames = append(metricNames, f)
		}
	}

	if !defaultTsDB.IsSplit() {
		defaultMetricNames = metricNames
	} else {
		//当指标类型为单指标单表时，则需要对每个指标检查是否有独立的路由配置
		for _, mName := range metricNames {
			sepRt := s.GetMetricSepRT(tableID, mName)
			if sepRt != nil {
				sepTsDB := defaultTsDB
				sepTsDB.DB = sepRt.DB
				sepTsDB.StorageID = strconv.FormatInt(sepRt.StorageId, 10)
				sepTsDB.ClusterName = sepRt.ClusterName
				sepTsDB.TagsKey = sepRt.TagsKey
				sepTsDB.Measurement = sepRt.Measurement
				sepTsDB.VmRt = sepRt.VmRt
				sepTsDB.ExpandMetricNames = []string{mName}
				tsDBs = append(tsDBs, &sepTsDB)
			} else {
				defaultMetricNames = append(defaultMetricNames, mName)
			}
		}
	}
	if len(defaultMetricNames) > 0 {
		defaultTsDB.ExpandMetricNames = defaultMetricNames
		tsDBs = append(tsDBs, &defaultTsDB)
	}
	return tsDBs
}

// GetMetricSepRT 获取指标独立配置的 RT
func (s *SpaceFilter) GetMetricSepRT(tableID string, metricName string) *routerInfluxdb.ResultTableDetail {
	route := strings.Split(tableID, ".")
	if len(route) != 2 {
		log.Errorf(s.ctx, "TableID(%s) format is wrong", tableID)
		return nil
	}
	// 按照固定路由规则来检索是否有独立配置的 RT
	sepRtID := fmt.Sprintf("%s.%s", route[0], metricName)
	rt := s.router.GetResultTable(s.ctx, sepRtID)
	return rt
}

func (s *SpaceFilter) GetSpaceRtInfo(tableID string) *routerInfluxdb.SpaceResultTable {
	if s.space == nil {
		return nil
	}
	v, _ := s.space[tableID]
	return v
}

func (s *SpaceFilter) GetSpaceRtIDs() []string {
	tIDs := make([]string, 0)
	if s.space != nil {
		for tID := range s.space {
			tIDs = append(tIDs, tID)
		}
	}
	return tIDs
}

func (s *SpaceFilter) DataList(tableID TableID, fieldName string, isRegexp bool) ([]*query.TsDBV2, error) {
	if tableID == "" && fieldName == "" {
		return nil, fmt.Errorf("%s, %s", ErrEmptyTableID.Error(), ErrMetricMissing.Error())
	}
	tsDBs := make([]*query.TsDBV2, 0)
	// 当空间为空时，无需进行下一步的检索
	if s.space == nil {
		return tsDBs, nil
	}

	// 判断 tableID 使用几段式
	db, measurement := tableID.Split()

	var fieldNameExp *regexp.Regexp
	if isRegexp {
		fieldNameExp = regexp.MustCompile(fieldName)
	}
	tableIDs := make([]string, 0)
	isK8s := false

	if db != "" && measurement != "" {
		// 判断如果 tableID 完整的情况下（三段式），则直接取对应的 tsDB
		tableIDs = append(tableIDs, string(tableID))
	} else if db != "" {
		// 指标二段式，仅传递 data-label
		tIDs := s.router.GetDataLabelRelatedRts(s.ctx, db)
		if tIDs != nil {
			tableIDs = tIDs
		}
	} else {
		// 如果不指定 tableID 或者 dataLabel，则检索跟字段相关的 RT，且只获取容器指标的 TsDB
		isK8s = true

		if fieldNameExp == nil {
			_tIDs := s.router.GetFieldRelatedRts(s.ctx, fieldName)
			if _tIDs != nil {
				tableIDs = _tIDs
			}
		} else {
			tableIDs = s.GetSpaceRtIDs()
		}
	}

	isK8sFeatureFlag := metadata.GetIsK8sFeatureFlag(s.ctx)

	for _, tID := range tableIDs {
		spaceRt := s.GetSpaceRtInfo(tID)
		if spaceRt == nil {
			continue
		}
		// 指标模糊匹配，可能命中多个私有指标 RT
		newTsDBs := s.NewTsDBs(spaceRt, fieldNameExp, fieldName, tID, isK8s, isK8sFeatureFlag)
		for _, newTsDB := range newTsDBs {
			tsDBs = append(tsDBs, newTsDB)
		}
	}

	if len(tsDBs) == 0 {
		msg := fmt.Sprintf(
			"spaceUid: %s and tableID: %s and fieldName: %s is not exists",
			s.spaceUid, tableID, fieldName,
		)
		metadata.SetStatus(s.ctx, metadata.SpaceTableIDFieldIsNotExists, msg)
		log.Warnf(s.ctx, msg)
	}
	return tsDBs, nil
}

type TsDBOption struct {
	SpaceUid  string
	TableID   TableID
	FieldName string
	// IsRegexp 指标是否使用正则查询
	IsRegexp bool
}

type TsDBs []*query.TsDBV2

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

	tsDBs, err := spaceFilter.DataList(option.TableID, option.FieldName, option.IsRegexp)
	if err != nil {
		return nil, err
	}
	return tsDBs, nil
}
