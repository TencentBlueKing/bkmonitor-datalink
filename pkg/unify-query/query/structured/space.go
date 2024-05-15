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
func NewSpaceFilter(ctx context.Context, opt *TsDBOption) (*SpaceFilter, error) {
	if opt == nil {
		return nil, fmt.Errorf("%s", ErrMetricMissing)
	}

	router, err := influxdb.GetSpaceTsDbRouter()
	if err != nil {
		return nil, err
	}

	var space routerInfluxdb.Space
	// 判断是否跳过空间限制
	if opt.IsSkipSpace {
		tableID := string(opt.TableID)
		space = map[string]*routerInfluxdb.SpaceResultTable{
			tableID: {
				TableId: tableID,
			},
		}
	} else {
		space = router.GetSpace(ctx, opt.SpaceUid)
	}

	if space == nil {
		msg := fmt.Sprintf("spaceUid: %s is not exists", opt.SpaceUid)
		metadata.SetStatus(ctx, metadata.SpaceIsNotExists, msg)
		log.Warnf(ctx, msg)
	}

	return &SpaceFilter{
		ctx:      ctx,
		spaceUid: opt.SpaceUid,
		router:   router,
		space:    space,
	}, nil
}

func (s *SpaceFilter) NewTsDBs(spaceTable *routerInfluxdb.SpaceResultTable, fieldNameExp *regexp.Regexp, conditions Conditions,
	fieldName, tableID string, isK8s, isK8sFeatureFlag, isSkipField bool) []*query.TsDBV2 {
	rtDetail := s.router.GetResultTable(s.ctx, tableID, false)
	if rtDetail == nil {
		log.Debugf(s.ctx, "skip rt(%s), rt detail is empty", tableID)
		return nil
	}

	// 只有在容器场景下的特殊逻辑
	if isK8s {
		// 增加在非单指标单表下，判断如果强行指定了单指标单表则对其进行修改以支持 vm 查询
		isSplitMeasurement := rtDetail.MeasurementType == redis.BkSplitMeasurement

		// 容器下只能查单指标单表
		if !isSplitMeasurement {
			log.Debugf(s.ctx, "skip rt(%s), measurement type (%s) is not split", tableID, rtDetail.MeasurementType)
			return nil
		}

		allConditions, err := conditions.AnalysisConditions()
		if err != nil {
			log.Errorf(s.ctx, "unable to get AllConditions, error: %s", err)
			return nil
		}

		// 容器下 bcs_cluster_id 是内置维度，才进行此逻辑判断
		// 如果 allConditions 中存在 clusterId 的筛选条件并且比对不成功的情况下，直接返回 nil，出现错误的情况也直接返回 nil
		compareResult, err := allConditions.Compare(ClusterID, rtDetail.BcsClusterID)
		if err != nil {
			log.Errorf(s.ctx, "allCondition Compare error: %s", err)
			return nil
		}

		if !compareResult {
			log.Debugf(s.ctx, "skip rt(%s), clusterID: %s, allConditions: %+v", tableID, rtDetail.BcsClusterID, allConditions)
			return nil
		}

		if isK8sFeatureFlag {
			// 如果是只查询 k8s 的 rt，则需要判断 bcsClusterID 字段不为空
			if rtDetail.BcsClusterID == "" {
				log.Debugf(s.ctx, "skip rt(%s), clusterID is empty", tableID)
				return nil
			}
		}
	}

	// 清理 filter 为空的数据
	filters := make([]query.Filter, 0, len(spaceTable.Filters))
	for _, f := range spaceTable.Filters {
		nf := make(map[string]string)
		for k, v := range f {
			if v != "" {
				nf[k] = v
			}
		}
		filters = append(filters, nf)
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
		StorageName:     rtDetail.StorageName,
		MetricName:      fieldName,
	}
	// 字段为空时，需要返回结果表的信息，表示无需过滤字段过滤
	// bklog 或者 bkapm 则不判断 field 是否存在
	if isSkipField {
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
	// 如果字段都不匹配到目标字段，则非目标结果表
	if len(metricNames) == 0 {
		return tsDBs
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
	// 如果这里出现指标列表为空，则说明指标都有独立的配置，不需要将默认的结果表配置写入
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
	rt := s.router.GetResultTable(s.ctx, sepRtID, true)
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

func (s *SpaceFilter) DataList(opt *TsDBOption) ([]*query.TsDBV2, error) {
	if opt == nil {
		return nil, fmt.Errorf("%s, %s", ErrEmptyTableID.Error(), ErrMetricMissing.Error())
	}

	if opt.TableID == "" && opt.FieldName == "" {
		return nil, fmt.Errorf("%s, %s", ErrEmptyTableID.Error(), ErrMetricMissing.Error())
	}
	tsDBs := make([]*query.TsDBV2, 0)
	// 当空间为空时，无需进行下一步的检索
	if s.space == nil {
		return tsDBs, nil
	}

	// 判断 tableID 使用几段式
	db, measurement := opt.TableID.Split()

	var fieldNameExp *regexp.Regexp
	if opt.IsRegexp {
		fieldNameExp = regexp.MustCompile(opt.FieldName)
	}
	tableIDs := make([]string, 0)
	isK8s := false

	if db != "" && measurement != "" {
		// 判断如果 tableID 完整的情况下（三段式），则直接取对应的 tsDB
		tableIDs = append(tableIDs, string(opt.TableID))
	} else if db != "" {
		// 指标二段式，仅传递 data-label
		tIDs := s.router.GetDataLabelRelatedRts(s.ctx, db)
		if tIDs != nil {
			tableIDs = tIDs
		}
	} else {
		// 如果不指定 tableID 或者 dataLabel，则检索跟字段相关的 RT，且只获取容器指标的 TsDB
		isK8s = true
		tableIDs = s.GetSpaceRtIDs()
	}

	isK8sFeatureFlag := metadata.GetIsK8sFeatureFlag(s.ctx)

	for _, tID := range tableIDs {
		spaceRt := s.GetSpaceRtInfo(tID)
		if spaceRt == nil {
			continue
		}
		// 指标模糊匹配，可能命中多个私有指标 RT
		newTsDBs := s.NewTsDBs(spaceRt, fieldNameExp, opt.Conditions, opt.FieldName, tID, isK8s, isK8sFeatureFlag, opt.IsSkipField)
		for _, newTsDB := range newTsDBs {
			tsDBs = append(tsDBs, newTsDB)
		}
	}

	if len(tsDBs) == 0 {
		msg := fmt.Sprintf(
			"spaceUid: %s and tableID: %s and fieldName: %s is not exists",
			s.spaceUid, opt.TableID, opt.FieldName,
		)
		// 当不存在前置异常，则需要在此处进行结论性记录
		if metadata.GetStatus(s.ctx) == nil {
			metadata.SetStatus(s.ctx, metadata.SpaceTableIDFieldIsNotExists, msg)
		}
		log.Warnf(s.ctx, msg)
	}
	return tsDBs, nil
}

type TsDBOption struct {
	SpaceUid    string
	IsSkipSpace bool
	IsSkipField bool

	TableID   TableID
	FieldName string
	// IsRegexp 指标是否使用正则查询
	IsRegexp   bool
	Conditions Conditions
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
	spaceFilter, err := NewSpaceFilter(ctx, option)
	if err != nil {
		return nil, err
	}

	tsDBs, err := spaceFilter.DataList(option)
	if err != nil {
		return nil, err
	}
	return tsDBs, nil
}
