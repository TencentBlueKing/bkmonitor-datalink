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
	"sort"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
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
	space = router.GetSpace(ctx, opt.SpaceUid)

	// 只有未跳过空间的时候进行异常判定
	if !opt.IsSkipSpace {
		if space == nil {
			metric.SpaceRouterNotExistInc(ctx, opt.SpaceUid, "", "", metadata.SpaceIsNotExists)

			msg := fmt.Sprintf("spaceUid: %s is not exists", opt.SpaceUid)
			metadata.SetStatus(ctx, metadata.SpaceIsNotExists, msg)
			log.Warnf(ctx, msg)
		}
	}

	return &SpaceFilter{
		ctx:      ctx,
		spaceUid: opt.SpaceUid,
		router:   router,
		space:    space,
	}, nil
}

func (s *SpaceFilter) getTsDBWithResultTableDetail(t query.TsDBV2, d *routerInfluxdb.ResultTableDetail) query.TsDBV2 {
	t.Field = d.Fields
	t.FieldAlias = d.FieldAlias
	t.MeasurementType = d.MeasurementType
	t.DataLabel = d.DataLabel
	t.StorageType = d.StorageType
	t.StorageID = strconv.Itoa(int(d.StorageId))
	t.ClusterName = d.ClusterName
	t.TagsKey = d.TagsKey
	t.DB = d.DB
	t.Measurement = d.Measurement
	t.VmRt = d.VmRt
	t.CmdbLevelVmRt = d.CmdbLevelVmRt
	t.StorageName = d.StorageName
	t.TimeField = metadata.TimeField{
		Name: d.Options.TimeField.Name,
		Type: d.Options.TimeField.Type,
		Unit: d.Options.TimeField.Unit,
	}
	t.NeedAddTime = d.Options.NeedAddTime
	t.SourceType = d.SourceType

	sort.SliceStable(d.StorageClusterRecords, func(i, j int) bool {
		return d.StorageClusterRecords[i].EnableTime > d.StorageClusterRecords[j].EnableTime
	})

	for _, record := range d.StorageClusterRecords {
		t.StorageClusterRecords = append(t.StorageClusterRecords, query.Record{
			StorageID:  strconv.Itoa(int(record.StorageID)),
			EnableTime: record.EnableTime,
		})
	}

	return t
}

func (s *SpaceFilter) NewTsDBs(spaceTable *routerInfluxdb.SpaceResultTable, fieldNameExp *regexp.Regexp, allConditions AllConditions,
	fieldName, tableID string, isK8s, isK8sFeatureFlag, isSkipField bool) []*query.TsDBV2 {

	var err error

	ctx, span := trace.NewSpan(s.ctx, "space-filter-new-ts-dbs")
	defer span.End(&err)

	rtDetail := s.router.GetResultTable(ctx, tableID, false)
	if rtDetail == nil {
		return nil
	}

	span.Set("result_table_detail", rtDetail)
	span.Set("is_k8s", isK8s)

	// 只有在容器场景下的特殊逻辑
	if isK8s {
		// 增加在非单指标单表下，判断如果强行指定了单指标单表则对其进行修改以支持 vm 查询
		isSplitMeasurement := rtDetail.MeasurementType == redis.BkSplitMeasurement

		// 容器下只能查单指标单表
		if !isSplitMeasurement {
			return nil
		}

		// 容器下 bcs_cluster_id 是内置维度，才进行此逻辑判断
		// 如果 allConditions 中存在 clusterId 的筛选条件并且比对不成功的情况下，直接返回 nil，出现错误的情况也直接返回 nil
		compareResult, err := allConditions.Compare(ClusterID, rtDetail.BcsClusterID)
		if err != nil {
			return nil
		}

		if !compareResult {
			return nil
		}

		if isK8sFeatureFlag {
			// 如果是只查询 k8s 的 rt，则需要判断 bcsClusterID 字段不为空
			if rtDetail.BcsClusterID == "" {
				return nil
			}
		}
	}

	// 清理 filter 为空的数据
	filters := make([]query.Filter, 0)
	if spaceTable != nil {
		for _, f := range spaceTable.Filters {
			nf := make(map[string]string)
			for k, v := range f {
				if v != "" {
					nf[k] = v
				}
			}
			filters = append(filters, nf)
		}
	}

	tsDBs := make([]*query.TsDBV2, 0)
	defaultMetricNames := make([]string, 0)

	// 原 Space(Type、BKDataID) 字段去掉，SegmentedEnable 设置了默认值 false
	// 原 Proxy(RetentionPolicies、BKBizID、DataID）直接去掉
	defaultTsDB := s.getTsDBWithResultTableDetail(query.TsDBV2{
		TableID:    tableID,
		Filters:    filters,
		MetricName: fieldName,
	}, rtDetail)

	// 字段为空时，需要返回结果表的信息，表示无需过滤字段过滤
	// bklog 或者 bkapm 则不判断 field 是否存在
	if isSkipField {
		defaultTsDB.ExpandMetricNames = []string{fieldName}
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
				defaultTsDB.ExpandMetricNames = []string{mName}
				sepTsDB := s.getTsDBWithResultTableDetail(defaultTsDB, sepRt)
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
		return &routerInfluxdb.SpaceResultTable{
			Filters: make([]map[string]string, 0),
		}
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
	var (
		routerMessage string
		err           error
	)

	defer func() {
		if routerMessage != "" {
			metric.SpaceRouterNotExistInc(s.ctx, opt.SpaceUid, string(opt.TableID), opt.FieldName, metadata.SpaceTableIDFieldIsNotExists)

			metadata.SetStatus(s.ctx, metadata.SpaceTableIDFieldIsNotExists, routerMessage)
			log.Warnf(s.ctx, routerMessage)
		}
	}()

	if opt == nil {
		return nil, fmt.Errorf("%s, %s", ErrEmptyTableID.Error(), ErrMetricMissing.Error())
	}

	if opt.TableID == "" && opt.FieldName == "" {
		return nil, fmt.Errorf("%s, %s", ErrEmptyTableID.Error(), ErrMetricMissing.Error())
	}
	tsDBs := make([]*query.TsDBV2, 0)
	// 当空间为空时，同时未跳过空间判断时，无需进行下一步的检索
	if !opt.IsSkipSpace {
		if s.space == nil {
			return tsDBs, nil
		}
	}

	// 判断 tableID 使用几段式
	db, measurement := opt.TableID.Split()

	var fieldNameExp *regexp.Regexp
	if opt.IsRegexp {
		fieldNameExp = regexp.MustCompile(opt.FieldName)
	}

	// tableID 去重，防止重复获取
	tableIDs := set.New[string]()
	isK8s := false

	ctx, span := trace.NewSpan(s.ctx, "space-filter-data-list")
	defer span.End(&err)

	if db != "" {
		// 指标二段式，仅传递 data-label， datalabel 支持各种格式
		tIDs := s.router.GetDataLabelRelatedRts(ctx, string(opt.TableID))
		tableIDs.Add(tIDs...)

		span.Set("data-label", opt.TableID)
		span.Set("data-label-table-id-list", tIDs)

		// 只有当 db 和 measurement 都不为空时，才是 tableID，为了兼容，同时也接入到 tableID  list
		if measurement != "" {
			tableIDs.Add(fmt.Sprintf("%s.%s", db, measurement))
		}

		if tableIDs.Size() == 0 {
			routerMessage = fmt.Sprintf("data_label router is empty with data_label: %s", db)
			return nil, nil
		}
	} else {
		// 如果不指定 tableID 或者 dataLabel，则检索跟字段相关的 RT，且只获取容器指标的 TsDB
		isK8s = !opt.IsSkipK8s
		tIDs := s.GetSpaceRtIDs()
		tableIDs.Add(tIDs...)

		if tableIDs.Size() == 0 {
			routerMessage = fmt.Sprintf("space is empty with spaceUid: %s", opt.SpaceUid)
			return nil, nil
		}
	}

	span.Set("table-id-list", tableIDs.ToArray())

	isK8sFeatureFlag := metadata.GetIsK8sFeatureFlag(s.ctx)

	for _, tID := range tableIDs.ToArray() {
		spaceRt := s.GetSpaceRtInfo(tID)
		// 如果不跳过空间，则取 space 和 tableIDs 的交集
		if !opt.IsSkipSpace && spaceRt == nil {
			continue
		}
		// 指标模糊匹配，可能命中多个私有指标 RT
		newTsDBs := s.NewTsDBs(spaceRt, fieldNameExp, opt.AllConditions, opt.FieldName, tID, isK8s, isK8sFeatureFlag, opt.IsSkipField)
		for _, newTsDB := range newTsDBs {
			tsDBs = append(tsDBs, newTsDB)
		}
	}

	if len(tsDBs) == 0 {
		routerMessage = fmt.Sprintf("tableID with field is empty with tableID: %s, field: %s, isSkipField: %v", opt.TableID, opt.FieldName, opt.IsSkipField)
		return nil, nil
	}

	return tsDBs, nil
}

type TsDBOption struct {
	SpaceUid    string
	IsSkipSpace bool
	IsSkipField bool
	IsSkipK8s   bool

	TableID   TableID
	FieldName string
	// IsRegexp 指标是否使用正则查询
	IsRegexp      bool
	AllConditions AllConditions
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
