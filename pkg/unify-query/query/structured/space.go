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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
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
		return nil, ErrMetricMissing
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
			metadata.NewMessage(
				metadata.MsgQueryRouter,
				"空间 %s 不存在",
				opt.SpaceUid,
			).Status(ctx, metadata.SpaceIsNotExists)
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
	fieldName, tableID string, isK8s, isK8sFeatureFlag, isSkipField bool, tableIDConditions AllConditions,
) ([]*query.TsDBV2, error) {
	rtDetail := s.router.GetResultTable(s.ctx, tableID, false)
	if rtDetail == nil {
		return nil, nil
	}
	// 仅在全空间候选路径下传入非空 tableIDConditions（见 DataList）；此处按结果表 Labels 过滤。
	if len(tableIDConditions) > 0 {
		labels := rtDetail.Labels
		if labels == nil {
			labels = make(map[string]string)
		}
		ok, err := tableIDConditions.MatchResultTableLabels(labels)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil
		}
	}

	// 容器场景下的特殊过滤逻辑：仅在未显式提供 table_id_conditions 时生效。
	// 若调用方已通过 table_id_conditions 按 Labels 显式路由，则信任该选择，不再叠加"仅 bk_split_measurement / BcsClusterID 匹配"的容器默认规则，
	// 避免把 bklog/bkapm 等非 split-measurement 的 RT 误过滤掉。
	if isK8s && len(tableIDConditions) == 0 {
		// 增加在非单指标单表下，判断如果强行指定了单指标单表则对其进行修改以支持 vm 查询
		isSplitMeasurement := rtDetail.MeasurementType == redis.BkSplitMeasurement

		// 容器下只能查单指标单表
		if !isSplitMeasurement {
			return nil, nil
		}

		// 容器下 bcs_cluster_id 是内置维度，才进行此逻辑判断
		// 如果 allConditions 中存在 clusterId 的筛选条件并且比对不成功的情况下，直接返回 nil，出现错误的情况也直接返回 nil
		compareResult, err := allConditions.Compare(ClusterID, rtDetail.BcsClusterID)
		if err != nil {
			return nil, nil
		}

		if !compareResult {
			return nil, nil
		}

		if isK8sFeatureFlag {
			// 如果是只查询 k8s 的 rt，则需要判断 bcsClusterID 字段不为空
			if rtDetail.BcsClusterID == "" {
				return nil, nil
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
		return tsDBs, nil
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
		return tsDBs, nil
	}

	if !defaultTsDB.IsSplit() {
		defaultMetricNames = metricNames
	} else {
		// 当指标类型为单指标单表时，则需要对每个指标检查是否有独立的路由配置
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

	return tsDBs, nil
}

// GetMetricSepRT 获取指标独立配置的 RT
func (s *SpaceFilter) GetMetricSepRT(tableID string, metricName string) *routerInfluxdb.ResultTableDetail {
	route := strings.Split(tableID, ".")
	if len(route) != 2 {
		metadata.NewMessage(
			metadata.MsgQueryRouter,
			"表ID格式不符合规范",
		).Warn(s.ctx)
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
	var routerMessage string

	defer func() {
		if routerMessage != "" {
			metric.SpaceRouterNotExistInc(s.ctx, opt.SpaceUid, string(opt.TableID), opt.FieldName, metadata.SpaceTableIDFieldIsNotExists)
			metadata.NewMessage(
				metadata.MsgQueryRouter,
				"%s",
				routerMessage,
			).Status(s.ctx, metadata.SpaceTableIDFieldIsNotExists)
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
	// 已指定 table_id / data_label（db 非空）时，候选 RT 已由 TableID 限定，不再按 table_id_conditions 过滤 Labels；
	// 未指定（db 为空，全空间扫表）时，才用 table_id_conditions 在候选集上按结果表 Labels 收窄。
	var tableIDCondsForFilter AllConditions
	if db == "" {
		tableIDCondsForFilter = opt.TableIDConditions
	}

	var fieldNameExp *regexp.Regexp
	if opt.IsRegexp {
		fieldNameExp = regexp.MustCompile(opt.FieldName)
	}

	// tableID 去重，防止重复获取
	tableIDs := set.New[string]()
	isK8s := false

	if db != "" {
		// 指标二段式，仅传递 data-label， datalabel 支持各种格式
		tIDs := s.router.GetDataLabelRelatedRts(s.ctx, string(opt.TableID))
		tableIDs.Add(tIDs...)

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

	isK8sFeatureFlag := featureFlag.GetIsK8sFeatureFlag(s.ctx)

	// 仅在启用了 table_id_conditions 时跟踪是否有候选 RT 的 Labels 命中，避免后续失败原因不在 Labels 时追加误导文案
	anyLabelMatched := len(tableIDCondsForFilter) == 0
	for _, tID := range tableIDs.ToArray() {
		spaceRt := s.GetSpaceRtInfo(tID)
		// 如果不跳过空间，则取 space 和 tableIDs 的交集
		if !opt.IsSkipSpace && spaceRt == nil {
			continue
		}
		if !anyLabelMatched {
			if rt := s.router.GetResultTable(s.ctx, tID, false); rt != nil {
				if tableIDCondsForFilter.MatchesResultTableLabels(rt.Labels) {
					anyLabelMatched = true
				}
			}
		}
		// 指标模糊匹配，可能命中多个私有指标 RT
		newTsDBs, err := s.NewTsDBs(spaceRt, fieldNameExp, opt.AllConditions, opt.FieldName, tID, isK8s, isK8sFeatureFlag, opt.IsSkipField, tableIDCondsForFilter)
		if err != nil {
			return nil, err
		}
		for _, newTsDB := range newTsDBs {
			tsDBs = append(tsDBs, newTsDB)
		}
	}

	if len(tsDBs) == 0 {
		routerMessage = fmt.Sprintf("tableID with field is empty with tableID: %s, field: %s, isSkipField: %v", opt.TableID, opt.FieldName, opt.IsSkipField)
		if len(tableIDCondsForFilter) > 0 && !anyLabelMatched {
			routerMessage += "；已启用 table_id_conditions，无 RT 的 Labels 命中条件"
		}
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
	// TableIDConditions 表标签条件（AllConditions）。仅当未指定 table_id / data_label（TableID.Split() 后 db 为空、走全空间候选）时参与过滤；已指定 db 或完整 table_id 时不生效。
	TableIDConditions AllConditions
}

type TsDBs []*query.TsDBV2

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
