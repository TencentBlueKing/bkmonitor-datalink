// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package service

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	mapset "github.com/deckarep/golang-set"
	"github.com/jinzhu/gorm"
	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/bcs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/recordrule"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/tenant"
	metadataMetrics "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/memcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mapx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/optionx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/stringx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type FilterUsage int

const (
	// UsageComposeData BKCC适用,组装业务关联数据路由
	UsageComposeData FilterUsage = iota

	// UsageComposeRecordRuleTableIds BKCC & BKCI & BKSAAS 适用,组装预计算数据
	UsageComposeRecordRuleTableIds
	// UsageComposeEsTableIds BKCC & BKCI & BKSAAS 适用,组装ES链路数据
	UsageComposeEsTableIds
	// UsageComposeEsBkciTableIds BKCI类型适用,组装BKCI关联的ES链路数据
	UsageComposeEsBkciTableIds
	// UsageComposeBcsSpaceBizTableIds BKCI类型适用,组装BKCI关联的业务数据
	UsageComposeBcsSpaceBizTableIds
	// UsageComposeBksaasSpaceClusterTableIds BKSAAS类型适用,组装BKSAAS关联的集群数据
	UsageComposeBksaasSpaceClusterTableIds
	// UsageComposeBcsSpaceClusterTableIds BKCI类型适用,组装BKCI关联的集群数据
	UsageComposeBcsSpaceClusterTableIds
	// UsageComposeBkciLevelTableIds BKCI类型适用,组装BKCI关联的全局数据
	UsageComposeBkciLevelTableIds
	// UsageComposeBkciOtherTableIds BKCI类型适用,组装BKCI关联的其他非集群数据
	UsageComposeBkciOtherTableIds
	// UsageComposeBkciCrossTableIds BKCI类型适用,组装BKCI关联的跨空间数据
	UsageComposeBkciCrossTableIds
	// UsageComposeBksaasOtherTableIds BKSAAS类型适用,组装BKSAAS关联的其他非集群数据
	UsageComposeBksaasOtherTableIds
	// UsageComposeAllTypeTableIds BKCI&BKSAAS类型 组装指定全空间的可以访问的结果表数据
	UsageComposeAllTypeTableIds
	UsageComposeDorisTableIds
)

type FilterBuildContext struct {
	SpaceType      string
	SpaceId        string
	TableId        string
	ClusterId      string
	NamespaceList  []string
	BkBizId        string // 真实的业务ID,如BKCI容器项目归属的业务ID
	IsShared       bool   // 用于判断共享集群
	ExtraStringVal string // 用于如 spaceObj.Id 这种转字符串场景
	FilterAlias    string // 过滤条件key的别名
}

// 基础数据表名
var BaseReportTableNames = []string{
	"cpu_summary",
	"cpu_detail",
	"disk",
	"env",
	"inode",
	"io",
	"load",
	"mem",
	"net",
	"netstat",
	"swap",
}

// 基础采集数据链路来源 -- 主机系统、DBM、DEVX、Perforce
const (
	BaseReportSourceSystem   = "sys"
	BaseReportSourceDBM      = "dbm"
	BaseReportSourceDevx     = "devx"
	BaseReportSourcePerforce = "perforce"
)

var BaseReportSources = []string{
	BaseReportSourceSystem,
	BaseReportSourceDBM,
	BaseReportSourceDevx,
	BaseReportSourcePerforce,
}

const (
	CachedClusterDataIdKey = "bmw_cached_cluster_data_id_list"
	CachedSpaceBizIdKey    = "bmw_cached_space_biz_id"
)

// RTFBatchQueryConfig ResultTableField 分批查询配置
type RTFBatchQueryConfig struct {
	BatchSize  int           // 每批查询的记录数量
	BatchDelay time.Duration // 批次间隔时间
}

// GetDefaultRTFBatchConfig 获取默认配置
func GetDefaultRTFBatchConfig() *RTFBatchQueryConfig {
	return &RTFBatchQueryConfig{
		BatchSize:  cfg.QueryDbBatchSize,  // 默认每批1000条记录
		BatchDelay: cfg.QueryDbBatchDelay, // 默认延迟20毫秒
	}
}

// SpaceRedisSvc 空间Redis service
type SpaceRedisSvc struct {
	goroutineLimit int
}

func (s *SpacePusher) buildFiltersByUsage(filterBuilderOptions FilterBuildContext, usage FilterUsage) []map[string]any {
	logger.Infof("buildFiltersByUsage: try to build space router fitlers for spaceType->[%s],spaceId->[%s],tableId->[%s],Usage->[%d]",
		filterBuilderOptions.SpaceType, filterBuilderOptions.SpaceId, filterBuilderOptions.TableId, usage)

	switch usage {

	case UsageComposeData: // BKCC类型,业务关联数据路由
		key := filterBuilderOptions.FilterAlias
		return []map[string]any{{key: filterBuilderOptions.SpaceId}}

	case UsageComposeBcsSpaceBizTableIds: // 适用于BKCI类型, BKCI空间关联的归属业务数据,如主机数据、插件数据等
		key := filterBuilderOptions.FilterAlias
		return []map[string]any{{key: filterBuilderOptions.BkBizId}} // 	这里拼接的过滤条件,是BKCI项目归属的业务ID

	case UsageComposeBkciLevelTableIds, UsageComposeBkciCrossTableIds: // BKCI类型,自身数据,关联跨空间数据
		key := filterBuilderOptions.FilterAlias
		return []map[string]any{{key: filterBuilderOptions.SpaceId}}

	case UsageComposeBkciOtherTableIds, UsageComposeBksaasOtherTableIds, UsageComposeRecordRuleTableIds, UsageComposeEsTableIds, UsageComposeEsBkciTableIds, UsageComposeDorisTableIds:
		return []map[string]any{}

	case UsageComposeAllTypeTableIds: // BKCI&BKSAAS类型，组装指定全空间的可以访问的结果表数据
		key := filterBuilderOptions.FilterAlias
		return []map[string]any{{key: filterBuilderOptions.ExtraStringVal}} // e.g. "-1001"

	case UsageComposeBksaasSpaceClusterTableIds, UsageComposeBcsSpaceClusterTableIds: // 适用于BKCI、BKSAAS类型,推送关联的集群数据,包括共享集群
		if filterBuilderOptions.IsShared && len(filterBuilderOptions.NamespaceList) > 0 {
			filters := make([]map[string]any, 0, len(filterBuilderOptions.NamespaceList))
			for _, ns := range filterBuilderOptions.NamespaceList {
				filters = append(filters, map[string]any{
					"bcs_cluster_id": filterBuilderOptions.ClusterId,
					"namespace":      ns,
				})
			}
			return filters
		}
		// 单集群
		return []map[string]any{{
			"bcs_cluster_id": filterBuilderOptions.ClusterId,
			"namespace":      nil,
		}}

	default:
		return []map[string]any{}
	}
}

func NewSpaceRedisSvc(goroutineLimit int) SpaceRedisSvc {
	if goroutineLimit <= 0 {
		goroutineLimit = 10
	}
	return SpaceRedisSvc{goroutineLimit: goroutineLimit}
}

type SpacePusher struct {
	mut  sync.Mutex
	once sync.Once
}

func NewSpacePusher() *SpacePusher {
	return &SpacePusher{}
}

// GetSpaceTableIdDataId 获取空间下的结果表和数据源信息
func (s *SpacePusher) GetSpaceTableIdDataId(bkTenantId string, spaceType, spaceId string, tableIdList []string, excludeDataIdList []uint, options *optionx.Options) (map[string]uint, error) {
	logger.Infof("GetSpaceTableIdDataId:space_type: %s, space_id: %s, table_id_list: %v, exclude_data_id_list: %v", spaceType, spaceId, tableIdList, excludeDataIdList)
	if options == nil {
		options = optionx.NewOptions(nil)
	}
	options.SetDefault("includePlatformDataId", true)
	db := mysql.GetDBSession().DB
	if len(tableIdList) != 0 {
		var dsrtList []resulttable.DataSourceResultTable
		for _, chunkTableIdList := range slicex.ChunkSlice(tableIdList, 0) {
			var tempList []resulttable.DataSourceResultTable
			qs := resulttable.NewDataSourceResultTableQuerySet(db).BkTenantIdEq(bkTenantId).TableIdIn(chunkTableIdList...)
			if len(excludeDataIdList) != 0 {
				qs = qs.BkDataIdNotIn(excludeDataIdList...)
			}
			if err := qs.All(&tempList); err != nil {
				logger.Errorf("GetSpaceTableIdDataId:query space [%s__%s] table [%v] data_id error, %v", spaceType, spaceId, tableIdList, err)
				return nil, err
			}
			dsrtList = append(dsrtList, tempList...)
		}
		dataMap := make(map[string]uint)
		for _, dsrt := range dsrtList {
			dataMap[dsrt.TableId] = dsrt.BkDataId
		}
		return dataMap, nil
	}
	// 否则，查询空间下的所有数据源，再过滤对应的结果表
	var spdsList []space.SpaceDataSource
	qs := space.NewSpaceDataSourceQuerySet(db).SpaceTypeIdEq(spaceType).SpaceIdEq(spaceId)

	// 获取是否授权数据
	if fromAuthorization, ok := options.GetBool("fromAuthorization"); ok {
		logger.Infof("GetSpaceTableIdDataId:fromAuthorization: %v,space_type: %s, space_id: %s, table_id_list: %v, exclude_data_id_list: %v", fromAuthorization, spaceType, spaceId, tableIdList, excludeDataIdList)
		qs = qs.FromAuthorizationEq(fromAuthorization)
	}
	if err := qs.All(&spdsList); err != nil {
		logger.Errorf("GetSpaceTableIdDataId:query space [%s__%s] data_id error, %v", spaceType, spaceId, err)
		return nil, err
	}
	dataIdSet := mapset.NewSet()
	for _, spds := range spdsList {
		dataIdSet.Add(spds.BkDataId)
	}
	// 过滤包含全局空间级的数据源
	if includePlatformDataId, _ := options.GetBool("includePlatformDataId"); includePlatformDataId {
		dataIds, err := s.getPlatformDataIds(bkTenantId, spaceType)
		if err != nil {
			return nil, err
		}
		dataIdSet = dataIdSet.Union(slicex.UintList2Set(dataIds))
	}

	// 排除元素
	if len(excludeDataIdList) != 0 {
		dataIdSet = dataIdSet.Difference(slicex.UintList2Set(excludeDataIdList))
	}
	dataIdList := slicex.UintSet2List(dataIdSet)
	if len(dataIdList) == 0 {
		logger.Errorf("GetSpaceTableIdDataId:space [%s__%s] data_id [%v] is empty", spaceType, spaceId, dataIdList)
		return map[string]uint{}, nil
	}
	dataMap := make(map[string]uint)
	var dsrtList []resulttable.DataSourceResultTable
	if err := resulttable.NewDataSourceResultTableQuerySet(db).BkTenantIdEq(bkTenantId).BkDataIdIn(dataIdList...).All(&dsrtList); err != nil {
		logger.Errorf("GetSpaceTableIdDataId:query space [%s__%s] data_id error, %v", spaceType, spaceId, err)
		return nil, err
	}
	for _, dsrt := range dsrtList {
		dataMap[dsrt.TableId] = dsrt.BkDataId
	}
	logger.Infof("GetSpaceTableIdDataId:space [%s__%s] data_id [%v] data_map [%v]", spaceType, spaceId, dataIdList, dataMap)
	return dataMap, nil
}

// PushDataLabelTableIds 推送 data_label 及对应的结果表
func (s *SpacePusher) PushDataLabelTableIds(bkTenantId string, tableIdList []string, isPublish bool) error {
	logger.Infof("PushDataLabelTableIds：start to push data_label table_id data")

	// 如果标签存在，则按照标签进行过滤
	dlRtsMap := make(map[string][]string)
	var err error
	// 1. 如果标签存在，则按照标签更新路由
	// 2. 如果结果表存在，则按照结果表更新路由
	// 3. 如果都不存在，则更新所有标签路由
	if len(tableIdList) != 0 {
		// 这里需要注意，因为是指定标签下所有更新，所以通过结果表查询到标签，再通过标签查询其下的所有结果表
		dataLabels, err := s.getDataLabelByTableId(bkTenantId, tableIdList)
		if err != nil {
			logger.Errorf("PushDataLabelTableIds end, get data label by table id error->[%s]", err)
			return err
		}
		dlRtsMap, err = s.getDataLabelTableIdMap(bkTenantId, dataLabels)
		if err != nil {
			logger.Errorf("PushDataLabelTableIds error->[%s]", err)
			return err
		}
	} else {
		dlRtsMap, err = s.getAllDataLabelTableId(bkTenantId)
		if err != nil {
			logger.Errorf("PushDataLabelTableIds: get all data label and table id map error->[%s]", err)
			return err
		}

		// 多租户模式下添加内置数据源
		if cfg.EnableMultiTenantMode {
		}

	}

	// 打印结束日志
	defer logger.Infof("PushDataLabelTableIds: push data_label table_id data successfully")

	// 如果数据标签和结果表的映射关系为空，则直接返回
	if len(dlRtsMap) == 0 {
		logger.Infof("PushDataLabelTableIds: data label and table id map is empty, skip push redis data_label_to_result_table,")
		return nil
	}

	client := redis.GetStorageRedisInstance()
	// TODO: 待旁路没有问题，可以移除的逻辑
	key := cfg.DataLabelToResultTableKey
	for dl, rts := range dlRtsMap {
		// 二段式补充
		for idx, value := range rts {
			rts[idx] = reformatTableId(value)
		}

		rtsStr, err := jsonx.MarshalString(rts)
		if err != nil {
			logger.Errorf("PushDataLabelTableIds: marshal data_label_to_result_table dl->[%s], rts->[%s], error->[%s]", dl, rts, err)
			return err
		}
		// NOTE:这里的HSetWithCompareAndPublish会判定新老值是否存在差异，若存在差异，则进行Publish操作
		logger.Infof("PushDataLabelTableIds: start push redis data_label_to_result_table, key->[%s], data_label->[%s], result_table->[%s], channel_name->[%s],channel_key->[%s]", key, dl, rtsStr, cfg.DataLabelToResultTableChannel, dl)
		isSuccess, err := client.HSetWithCompareAndPublish(key, dl, rtsStr, cfg.DataLabelToResultTableChannel, dl)
		if err != nil {
			logger.Errorf("PushDataLabelTableIds: push redis data_label_to_result_table error, dl->[%s], rts->[%s], error->[%s]", dl, rts, err)
			return err
		}
		logger.Infof("PushDataLabelTableIds: push redis data_label_to_result_table and publish, data_label->[%s], result_table->[%s], isSuccess->[%v]", dl, rtsStr, isSuccess)
	}

	return nil
}

// getDataLabelTableIdMap 获取数据标签和结果表的映射关系
func (s *SpacePusher) getDataLabelTableIdMap(bkTenantId string, dataLabelList []string) (map[string][]string, error) {
	if len(dataLabelList) == 0 {
		return nil, errors.New("data label is null")
	}

	// dataLabelList 可能存在重复，需要去重
	dataLabelSet := make(map[string]struct{})
	for _, dataLabel := range dataLabelList {
		dataLabelSet[dataLabel] = struct{}{}
	}

	var rts []resulttable.ResultTable

	// 由于 data_label 可能存在逗号分隔传多个标签的情况，所以无法直接搜索，只能先查询全部结果表，再过滤数据标签
	if err := resulttable.NewResultTableQuerySet(mysql.GetDBSession().DB).Select(resulttable.ResultTableDBSchema.TableId, resulttable.ResultTableDBSchema.DataLabel, resulttable.ResultTableDBSchema.BkTenantId).DataLabelNe("").DataLabelIsNotNull().IsDeletedEq(false).IsEnableEq(true).BkTenantIdEq(bkTenantId).All(&rts); err != nil {
		logger.Errorf("get table id by data label error, %s", err)
		return nil, errors.Wrap(err, "get table id by data label error")
	}

	// 如果结果表为空，则直接返回
	if len(rts) == 0 {
		return nil, errors.Errorf("not found table id by data label, data labels: %v", dataLabelList)
	}

	dlRtsMap := make(map[string][]string)
	for _, rt := range rts {
		// 如果数据标签为空，则跳过
		if rt.DataLabel == nil || *rt.DataLabel == "" {
			continue
		}

		// 数据标签可能存在多个，需要拆分
		for _, dataLabel := range strings.Split(*rt.DataLabel, ",") {
			// 如果数据标签为空，则跳过
			if dataLabel == "" {
				continue
			}

			// 判断是否在 dataLabelSet 中
			if _, ok := dataLabelSet[dataLabel]; !ok {
				continue
			}

			var key string
			// 多租户模式下，需要加上租户ID后缀
			if cfg.EnableMultiTenantMode {
				key = fmt.Sprintf("%s|%s", dataLabel, rt.BkTenantId)
			} else {
				key = dataLabel
			}
			if rts, ok := dlRtsMap[key]; ok {
				dlRtsMap[key] = append(rts, rt.TableId)
			} else {
				dlRtsMap[key] = []string{rt.TableId}
			}
		}
	}
	return dlRtsMap, nil
}

// getDataLabelByTableId 获取结果表对应的数据标签
func (s *SpacePusher) getDataLabelByTableId(bkTenantId string, tableIdList []string) ([]string, error) {
	if len(tableIdList) == 0 {
		return nil, errors.Errorf("table id is null")
	}
	db := mysql.GetDBSession().DB
	var dataLabels []resulttable.ResultTable
	for _, chunkTableIds := range slicex.ChunkSlice(tableIdList, 0) {
		var tempList []resulttable.ResultTable
		if err := resulttable.NewResultTableQuerySet(db).Select(resulttable.ResultTableDBSchema.DataLabel, resulttable.ResultTableDBSchema.BkTenantId).DataLabelNe("").DataLabelIsNotNull().BkTenantIdEq(bkTenantId).TableIdIn(chunkTableIds...).All(&tempList); err != nil {
			logger.Errorf("get table id by data label error, %s", err)
			continue
		}
		dataLabels = append(dataLabels, tempList...)
	}
	if len(dataLabels) == 0 {
		return nil, errors.Errorf("not found data label by table id, table ids: %v", tableIdList)
	}
	var dataLabelList []string
	for _, dl := range dataLabels {
		// 如果数据标签为空，则跳过
		if dl.DataLabel == nil || *dl.DataLabel == "" {
			continue
		}
		for _, dataLabel := range strings.Split(*dl.DataLabel, ",") {
			// 如果数据标签为空，则跳过
			if dataLabel == "" {
				continue
			}
			dataLabelList = append(dataLabelList, dataLabel)
		}
	}
	dataLabelList = slicex.RemoveDuplicate(&dataLabelList)
	return dataLabelList, nil
}

// 获取所有标签和结果表的映射关系
func (s *SpacePusher) getAllDataLabelTableId(bkTenantId string) (map[string][]string, error) {
	// 获取所有可用的结果表
	db := mysql.GetDBSession().DB
	var rtList []resulttable.ResultTable
	// 过滤为结果表可用，标签不为空和null的数据记录
	if err := resulttable.NewResultTableQuerySet(db).Select(resulttable.ResultTableDBSchema.BkTenantId, resulttable.ResultTableDBSchema.TableId, resulttable.ResultTableDBSchema.DataLabel).BkTenantIdEq(bkTenantId).IsEnableEq(true).IsDeletedEq(false).DataLabelIsNotNull().DataLabelNe("").All(&rtList); err != nil {
		logger.Errorf("get all data label and table id map error, %s", err)
		return nil, err
	}
	// 获取结果表
	dataLabelTableIdMap := make(map[string][]string)
	for _, rt := range rtList {
		// 如果数据标签为空，则跳过
		if rt.DataLabel == nil || *rt.DataLabel == "" {
			continue
		}

		// 数据标签可能存在多个，需要拆分
		dataLabels := strings.Split(*rt.DataLabel, ",")
		for _, dataLabel := range dataLabels {
			// 如果数据标签为空，则跳过
			if dataLabel == "" {
				continue
			}

			var key string
			if cfg.EnableMultiTenantMode {
				key = fmt.Sprintf("%s|%s", dataLabel, bkTenantId)
			} else {
				key = dataLabel
			}

			if rts, ok := dataLabelTableIdMap[key]; ok {
				dataLabelTableIdMap[key] = append(rts, rt.TableId)
			} else {
				dataLabelTableIdMap[key] = []string{rt.TableId}
			}
		}
	}

	// 多租户模式特殊处理 -- 内置系统数据路由
	if cfg.EnableMultiTenantMode {
		var builtInRts []resulttable.ResultTable

		// 表名格式为{bk_tenant_id}_{bk_biz_id}_{source}.{table}，先进行粗略匹配
		tablePattern := fmt.Sprintf("%s_%%_%%.%%", bkTenantId)
		if err := resulttable.NewResultTableQuerySet(db).Select(resulttable.ResultTableDBSchema.TableId).TableIdLike(tablePattern).BkTenantIdEq(bkTenantId).IsEnableEq(true).IsDeletedEq(false).All(&builtInRts); err != nil {
			logger.Errorf("get built in data label and table id map error, %s", err)
			return nil, err
		}

		tableRegex := fmt.Sprintf("^%s_\\d+_(%s)\\.(%s)$", bkTenantId, strings.Join(BaseReportSources, "|"), strings.Join(BaseReportTableNames, "|"))
		re := regexp.MustCompile(tableRegex)
		for _, rt := range builtInRts {
			// 严格匹配正则表达式
			if !re.MatchString(rt.TableId) {
				continue
			}

			// 提取source和table
			matches := re.FindStringSubmatch(rt.TableId)
			if len(matches) != 3 {
				continue
			}
			source := matches[1]
			tableName := matches[2]

			// 根据source和table生成数据标签
			var dataLabel string
			if source == BaseReportSourceSystem {
				// 系统数据标签
				dataLabel = fmt.Sprintf("system.%s", tableName)
			} else {
				// 其他数据标签
				dataLabel = fmt.Sprintf("%s_system.%s", source, tableName)
			}

			// 多租户模式下，data_label需要加上租户ID后缀
			key := fmt.Sprintf("%s|%s", dataLabel, bkTenantId)

			// 添加到数据标签和结果表的映射关系中
			if rts, ok := dataLabelTableIdMap[key]; ok {
				dataLabelTableIdMap[key] = append(rts, rt.TableId)
			} else {
				dataLabelTableIdMap[key] = []string{rt.TableId}
			}
		}
	}

	return dataLabelTableIdMap, nil
}

// 提取具备VM、ES、InfluxDB链路的结果表
func (s *SpacePusher) refineTableIds(tableIdList []string) ([]string, error) {
	db := mysql.GetDBSession().DB
	// 过滤写入 influxdb 的结果表
	var influxdbStorageList []storage.InfluxdbStorage
	qs := storage.NewInfluxdbStorageQuerySet(db).Select(storage.InfluxdbStorageDBSchema.TableID)
	if len(tableIdList) != 0 {
		for _, chunkTableIdList := range slicex.ChunkSlice(tableIdList, 0) {
			var tempList []storage.InfluxdbStorage

			qsTemp := qs.TableIDIn(chunkTableIdList...)
			if err := qsTemp.All(&tempList); err != nil {
				return nil, err
			}
			influxdbStorageList = append(influxdbStorageList, tempList...)
		}
	} else {
		if err := qs.All(&influxdbStorageList); err != nil {
			return nil, err
		}
	}

	// 过滤写入 vm 的结果表
	var vmRecordList []storage.AccessVMRecord
	qs2 := storage.NewAccessVMRecordQuerySet(db).Select(storage.AccessVMRecordDBSchema.ResultTableId)
	if len(tableIdList) != 0 {
		for _, chunkTableIdList := range slicex.ChunkSlice(tableIdList, 0) {
			var tempList []storage.AccessVMRecord
			qsTemp := qs2.ResultTableIdIn(chunkTableIdList...)
			if err := qsTemp.All(&tempList); err != nil {
				return nil, err
			}
			vmRecordList = append(vmRecordList, tempList...)
		}
	} else {
		if err := qs2.All(&vmRecordList); err != nil {
			return nil, err
		}
	}

	// 过滤写入 ES 的结果表
	var esStorageList []storage.ESStorage
	qs3 := storage.NewESStorageQuerySet(db).Select(storage.ESStorageDBSchema.TableID)
	if len(tableIdList) != 0 {
		for _, chunkTableIdList := range slicex.ChunkSlice(tableIdList, 0) {
			var tempList []storage.ESStorage
			qsTemp := qs3.TableIDIn(chunkTableIdList...)
			if err := qsTemp.All(&tempList); err != nil {
				return nil, err
			}
			esStorageList = append(esStorageList, tempList...)
		}
	} else {
		if err := qs3.All(&esStorageList); err != nil {
			return nil, err
		}
	}

	// 合并所有表 ID
	var tableIds []string
	for _, i := range influxdbStorageList {
		tableIds = append(tableIds, i.TableID)
	}
	for _, i := range vmRecordList {
		tableIds = append(tableIds, i.ResultTableId)
	}
	for _, i := range esStorageList {
		tableIds = append(tableIds, i.TableID)
	}

	// 去重
	tableIds = slicex.RemoveDuplicate(&tableIds)
	return tableIds, nil
}

// PushTableIdDetail 推送结果表的详细信息
func (s *SpacePusher) PushTableIdDetail(bkTenantId string, tableIdList []string, isPublish bool) error {
	logger.Infof("PushTableIdDetail: start to push table_id detail data")

	if len(tableIdList) == 0 {
		logger.Infof("PushTableIdDetail: table_id_list is empty, query all table_id")
	}

	tableIdDetail, err := s.getTableInfoForInfluxdbAndVm(bkTenantId, tableIdList)
	logger.Infof("PushTableIdDetail: get table info for influxdb and vm:%s", tableIdDetail)
	if err != nil {
		return err
	}
	if len(tableIdDetail) == 0 {
		logger.Infof("PushTableIdDetail: not found table from influxdb or vm")
		return nil
	}
	var tableIds []string
	for tableId := range tableIdDetail {
		tableIds = append(tableIds, tableId)
	}
	db := mysql.GetDBSession().DB
	// 获取结果表类型
	var rtList []resulttable.ResultTable
	if err := resulttable.NewResultTableQuerySet(db).Select(resulttable.ResultTableDBSchema.TableId, resulttable.ResultTableDBSchema.SchemaType, resulttable.ResultTableDBSchema.DataLabel).BkTenantIdEq(bkTenantId).TableIdIn(tableIds...).All(&rtList); err != nil {
		return err
	}
	tableIdRtMap := make(map[string]resulttable.ResultTable)
	for _, rt := range rtList {
		tableIdRtMap[rt.TableId] = rt
	}

	var dsrtList []resulttable.DataSourceResultTable
	if err := resulttable.NewDataSourceResultTableQuerySet(db).Select(resulttable.DataSourceResultTableDBSchema.TableId, resulttable.DataSourceResultTableDBSchema.BkDataId).BkTenantIdEq(bkTenantId).TableIdIn(tableIds...).All(&dsrtList); err != nil {
		return err
	}
	tableIdDataIdMap := make(map[string]uint)
	for _, dsrt := range dsrtList {
		tableIdDataIdMap[dsrt.TableId] = dsrt.BkDataId
	}

	// 获取结果表对应的类型
	measurementTypeMap, err := s.getMeasurementTypeByTableId(bkTenantId, tableIds, rtList, tableIdDataIdMap)
	if err != nil {
		logger.Errorf("PushTableIdDetail: get measurement type by table id failed, err: %s", err.Error())
		return err
	}
	// 再追加上结果表的指标数据、集群 ID、类型
	tableIdClusterIdMap, err := s.getTableIdClusterId(bkTenantId, tableIds)
	if err != nil {
		logger.Errorf("PushTableIdDetail: get table id cluster id failed, err: %s", err.Error())
		return err
	}
	tableIdFields, err := s.composeTableIdFields(bkTenantId, tableIds)
	if err != nil {
		logger.Errorf("PushTableIdDetail: compose table id fields failed, err: %s", err.Error())
		return err
	}

	client := redis.GetStorageRedisInstance()
	// 推送数据
	rtDetailKey := cfg.ResultTableDetailKey
	for tableId, detail := range tableIdDetail {
		var ok bool
		// fields
		detail["fields"], ok = tableIdFields[tableId]
		metricNum := 0
		if !ok {
			detail["fields"] = []string{}
		} else {
			metricNum = len(tableIdFields[tableId])
		}
		// 添加结果表的指标数量
		metadataMetrics.RtMetricNum(tableId, float64(metricNum))

		// 多租户模式下，需要加上租户ID后缀
		var redisKey string
		if cfg.EnableMultiTenantMode {
			redisKey = fmt.Sprintf("%s|%s", tableId, bkTenantId)
		} else {
			redisKey = tableId
		}

		// data_label
		rt, ok := tableIdRtMap[tableId]
		if !ok {
			detail["data_label"] = ""
		} else {
			detail["data_label"] = rt.DataLabel
		}
		detail["measurement_type"] = measurementTypeMap[tableId]
		detail["bcs_cluster_id"] = tableIdClusterIdMap[tableId]
		detail["bk_data_id"] = tableIdDataIdMap[tableId]
		detailStr, err := jsonx.MarshalString(detail)
		if err != nil {
			logger.Errorf("PushTableIdDetail:marshal result_table_detail failed, table_id: %s, err: %s", tableId, err.Error())
			return err
		}

		// NOTE:这里的HSetWithCompareAndPublish会判定新老值是否存在差异，若存在差异，则进行Publish操作
		// NOTE:这里统一根据Redis中的新老值是否存在差异决定是否需要Publish
		logger.Infof("PushTableIdDetail:start push and publish redis result_table_detail, table_id[%s],channel_name->[%s],channel_key->[%s]", tableId, cfg.ResultTableDetailChannel, tableId)
		isSuccess, err := client.HSetWithCompareAndPublish(rtDetailKey, redisKey, detailStr, cfg.ResultTableDetailChannel, redisKey)
		if err != nil {
			logger.Errorf("PushTableIdDetail:push and publish redis result_table_detail failed, table_id: %s, err: %s", tableId, err.Error())
			return err
		}
		logger.Infof("PushTableIdDetail:push redis result_table_detail success, table_id->[%s],isSuccess->[%v]", tableId, isSuccess)
	}

	logger.Info("PushTableIdDetail:push redis result_table_detail")
	return nil
}

// PushEsTableIdDetail compose the es table id detail
func (s *SpacePusher) PushEsTableIdDetail(tableIdList []string, isPublish bool) error {
	logger.Infof("PushEsTableIdDetail:start to compose es table id detail data, table_id_list [%v]", tableIdList)
	db := mysql.GetDBSession().DB

	// 获取数据
	var esStorageList []storage.ESStorage
	esQuerySet := storage.NewESStorageQuerySet(db).Select(
		storage.ESStorageDBSchema.TableID,
		storage.ESStorageDBSchema.StorageClusterID,
		storage.ESStorageDBSchema.SourceType,
		storage.ESStorageDBSchema.IndexSet,
	)

	// 如果过滤结果表存在，则添加过滤条件
	if len(tableIdList) != 0 {
		if err := esQuerySet.TableIDIn(tableIdList...).All(&esStorageList); err != nil {
			logger.Errorf("PushEsTableIdDetail:compose es table id detail error, table_id: %v, error: %s", tableIdList, err)
			return err
		}
	} else {
		if err := esQuerySet.All(&esStorageList); err != nil {
			logger.Errorf("PushEsTableIdDetail:compose es table id detail error, %s", err)
			return err
		}
	}

	// 查询es结果表的 option
	var tidList []string
	for _, es := range esStorageList {
		tidList = append(tidList, es.TableID)
	}
	// 组装结果表对应的选项
	tidOptionMap := s.composeEsTableIdOptions(tidList)

	// 获取查询别名映射关系
	fieldAliasMap, err := s.getFieldAliasMap(tidList)
	if err != nil {
		logger.Errorf("PushEsTableIdDetail: failed to get field alias map, error: %s", err)
	}

	// 组装数据
	client := redis.GetStorageRedisInstance()
	wg := &sync.WaitGroup{}
	// 因为每个处理任务完全独立，可以并发执行
	ch := make(chan struct{}, 50)
	wg.Add(len(esStorageList))
	for _, es := range esStorageList {
		// 获取 option 数据
		options, ok := tidOptionMap[es.TableID]
		if !ok {
			options = make(map[string]any)
		}
		ch <- struct{}{}
		go func(es storage.ESStorage, options map[string]any, wg *sync.WaitGroup, ch chan struct{}) {
			defer func() {
				<-ch
				wg.Done()
			}()

			tableId := es.TableID

			sourceType := es.SourceType
			indexSet := es.IndexSet
			logger.Infof("PushEsTableIdDetail:start to compose es table id detail, table_id->[%s],source_type->[%s],index_set->[%s]", tableId, sourceType, indexSet)

			var fieldAliasSettings map[string]string
			if fieldAliasMap != nil {
				fieldAliasSettings = fieldAliasMap[tableId]
			}

			composedTableId, detailStr, err := s.composeEsTableIdDetail(tableId, options, es.StorageClusterID, sourceType, indexSet, fieldAliasSettings)
			if err != nil {
				logger.Errorf("PushEsTableIdDetail:compose es table id detail error, table_id: %s, error: %s", tableId, err)
				return
			}
			// 推送数据
			// NOTE: HSetWithCompareAndPublish 判定新老值是否存在差异，若存在差异，则进行 Publish 操作
			logger.Infof("PushEsTableIdDetail:start push and publish es table id detail, table_id->[%s],channel_name->[%s],channel_key->[%s],detail->[%v]", composedTableId, cfg.ResultTableDetailChannel, composedTableId, detailStr)
			isSuccess, err := client.HSetWithCompareAndPublish(cfg.ResultTableDetailKey, composedTableId, detailStr, cfg.ResultTableDetailChannel, composedTableId)
			if err != nil {
				logger.Errorf("PushEsTableIdDetail:push and publish es table id detail error, table_id->[%s], error->[%s]", tableId, err)
				return
			}
			logger.Infof("PushEsTableIdDetail:push es table id detail success, table_id->[%s], is_success->[%v]", tableId, isSuccess)
		}(es, options, wg, ch)
	}
	wg.Wait()
	logger.Infof("PushEsTableIdDetail:push es table id detail success, table_id_list [%v]", tableIdList)
	return nil
}

// PushDorisTableIdDetail  推送Doris结果表详情路由
func (s *SpacePusher) PushDorisTableIdDetail(tableIdList []string, isPublish bool) error {
	logger.Infof("PushDorisTableIdDetail:start to compose doris table id detail data")
	db := mysql.GetDBSession().DB

	// 获取数据
	var dorisStorageList []storage.DorisStorage
	dorisQuerySet := storage.NewDorisStorageQuerySet(db).Select(
		storage.DorisStorageDBSchema.TableID,
		storage.DorisStorageDBSchema.BkbaseTableID,
	)

	// 如果过滤结果表存在，则添加过滤条件
	if len(tableIdList) != 0 {
		if err := dorisQuerySet.TableIDIn(tableIdList...).All(&dorisStorageList); err != nil {
			logger.Errorf("PushDorisTableIdDetail: compose doris table id detail error, table_id: %v, error: %s", tableIdList, err)
			return err
		}
	} else {
		if err := dorisQuerySet.All(&dorisStorageList); err != nil {
			logger.Errorf("PushDorisTableIdDetail: compose doris table id detail error, %s", err)
			return err
		}
	}

	var tidList []string
	for _, doris := range dorisStorageList {
		tidList = append(tidList, doris.TableID)
	}

	// 获取查询别名映射关系
	fieldAliasMap, err := s.getFieldAliasMap(tidList)
	if err != nil {
		logger.Errorf("PushDorisTableIdDetail: failed to get field alias map, error: %s", err)
	}

	// 组装数据
	client := redis.GetStorageRedisInstance()
	wg := &sync.WaitGroup{}
	// 因为每个处理任务完全独立，可以并发执行
	ch := make(chan struct{}, 50)
	wg.Add(len(dorisStorageList))
	for _, doris := range dorisStorageList {
		ch <- struct{}{}
		go func(doris storage.DorisStorage, wg *sync.WaitGroup, ch chan struct{}) {
			defer func() {
				<-ch
				wg.Done()
			}()

			tableId := doris.TableID
			bkbaseTableId := doris.BkbaseTableID

			logger.Infof("PushDorisTableIdDetail:start to compose doris table id detail, table_id->[%s],bkbase_table_id->[%s]", tableId, bkbaseTableId)

			var fieldAliasSettings map[string]string
			if fieldAliasMap != nil {
				fieldAliasSettings = fieldAliasMap[tableId]
			}

			composedTableId, detailStr, err := s.composeDorisTableIdDetail(tableId, doris.BkbaseTableID, fieldAliasSettings)
			if err != nil {
				logger.Errorf("PushDorisTableIdDetail:compose doris table id detail error, table_id: %s, error: %s", tableId, err)
				return
			}
			// 推送数据
			// NOTE: HSetWithCompareAndPublish 判定新老值是否存在差异，若存在差异，则进行 Publish 操作
			logger.Infof("PushDorisTableIdDetail:start push and publish doris table id detail, table_id->[%s],channel_name->[%s],channel_key->[%s],detail->[%v]", composedTableId, cfg.ResultTableDetailChannel, composedTableId, detailStr)
			isSuccess, err := client.HSetWithCompareAndPublish(cfg.ResultTableDetailKey, composedTableId, detailStr, cfg.ResultTableDetailChannel, composedTableId)
			if err != nil {
				logger.Errorf("PushDorisTableIdDetail:push and publish doris table id detail error, table_id->[%s], error->[%s]", tableId, err)
				return
			}
			logger.Infof("PushDorisTableIdDetail: push doris table id detail success, table_id->[%s], is_success->[%v]", tableId, isSuccess)
		}(doris, wg, ch)
	}
	wg.Wait()
	logger.Infof("PushDorisTableIdDetail: push doris table id detail success, table_id_list [%v]", tableIdList)
	return nil
}

// composeEsTableIdOptions 组装 es
func (s *SpacePusher) composeEsTableIdOptions(tableIdList []string) map[string]map[string]any {
	db := mysql.GetDBSession().DB
	// 分批获取结果表的option
	tidOptionMap := make(map[string]map[string]any)
	for _, chunkTableIdList := range slicex.ChunkSlice(tableIdList, 0) {
		var tempList []resulttable.ResultTableOption
		if err := resulttable.NewResultTableOptionQuerySet(db).Select(resulttable.ResultTableOptionDBSchema.TableID, resulttable.ResultTableOptionDBSchema.Name, resulttable.ResultTableOptionDBSchema.Value).TableIDIn(chunkTableIdList...).All(&tempList); err != nil {
			logger.Errorf("query result table option error, error: %s", err)
			continue
		}
		for _, option := range tempList {
			tidOption, ok := tidOptionMap[option.TableID]

			var opValue any
			opValue, err := option.InterfaceValue()
			if err != nil {
				logger.Errorf("unmarshal result table option value error, table_id: %s, option_value: %s, error: %s", option.TableID, option.Value, err)
				opValue = make(map[string]any)
			}
			// 如果已经存在，则追加数据
			if ok {
				tidOption[option.Name] = opValue
				tidOptionMap[option.TableID] = tidOption
			} else {
				// 否则，直接赋值
				tidOptionMap[option.TableID] = map[string]any{option.Name: opValue}
			}
		}
	}
	return tidOptionMap
}

// getFieldAliasMap 构建字段别名映射map
func (s *SpacePusher) getFieldAliasMap(tableIDList []string) (map[string]map[string]string, error) {
	logger.Infof("getFieldAliasMap: try to get field alias map, table_id_list->[%v]", tableIDList)

	db := mysql.GetDBSession().DB

	if len(tableIDList) == 0 {
		return make(map[string]map[string]string), nil
	}

	// 获取指定table_id列表的未删除别名记录
	var aliasRecords []resulttable.ESFieldQueryAliasOption

	fieldAliasQuerySet := resulttable.NewESFieldQueryAliasOptionQuerySet(db).Select(
		resulttable.ESFieldQueryAliasOptionDBSchema.TableID,
		resulttable.ESFieldQueryAliasOptionDBSchema.FieldPath,
		resulttable.ESFieldQueryAliasOptionDBSchema.QueryAlias,
		resulttable.ESFieldQueryAliasOptionDBSchema.IsDeleted,
	)

	err := fieldAliasQuerySet.TableIDIn(tableIDList...).IsDeletedEq(false).All(&aliasRecords)
	if err != nil {
		logger.Errorf("getFieldAliasMap: Error getting field alias map for table_ids: %v, error: %v", tableIDList, err)
		return nil, err
	}

	// 按table_id分组构建别名映射
	fieldAliasMap := make(map[string]map[string]string)
	for _, record := range aliasRecords {
		tableID := record.TableID
		queryAlias := record.QueryAlias
		fieldPath := record.FieldPath

		// 验证数据完整性
		if tableID == "" || queryAlias == "" || fieldPath == "" {
			logger.Warnf("getFieldAliasMap: invalid alias record, skipping - table_id: %s, query_alias: %s, field_path: %s",
				tableID, queryAlias, fieldPath)
			continue
		}

		if fieldAliasMap[tableID] == nil {
			fieldAliasMap[tableID] = make(map[string]string)
		}

		fieldAliasMap[tableID][queryAlias] = fieldPath
	}

	logger.Infof("getFieldAliasMap: Field alias map generated: %+v", fieldAliasMap)
	return fieldAliasMap, nil
}

func (s *SpacePusher) composeEsTableIdDetail(tableId string, options map[string]any, storageClusterId uint, sourceType, indexSet string, fieldAliasSettings map[string]string) (string, string, error) {
	logger.Infof("compose es table id detail, table_id [%s], options [%+v], storage_cluster_id [%d], source_type [%s], index_set [%s]", tableId, options, storageClusterId, sourceType, indexSet)

	// 获取历史存储集群记录
	db := mysql.GetDBSession().DB

	// 若该RT是虚拟RT,则使用其关联的真实RT去查询存储集群记录信息
	var storageIns storage.ESStorage
	realTableId := tableId
	if err := storage.NewESStorageQuerySet(db).Select(storage.ESStorageDBSchema.OriginTableId).TableIDEq(tableId).One(&storageIns); err != nil {
		logger.Errorf("composeEsTableIdDetail: failed to get origin table_id for table_id [%s], error: %v", tableId, err)
		return tableId, "", err
	}

	if storageIns.OriginTableId != "" {
		logger.Infof("composeEsTableIdDetail: origin table_id [%s] found for table_id [%s]", storageIns.OriginTableId, tableId)
		realTableId = storageIns.OriginTableId
	}

	clusterRecords, err := storage.ComposeTableIDStorageClusterRecords(db, realTableId)
	if err != nil {
		logger.Errorf("composeEsTableIdDetail: failed to get storage cluster records for table_id [%s], error: %v", realTableId, err)
		return "", "", err
	}

	var rt resulttable.ResultTable
	if err := resulttable.NewResultTableQuerySet(db).Select(resulttable.ResultTableDBSchema.DataLabel).TableIdEq(tableId).One(&rt); err != nil {
		return tableId, "", err
	}

	if fieldAliasSettings == nil {
		fieldAliasSettings = make(map[string]string)
	}

	// 组装数据
	detailStr, err := jsonx.MarshalString(map[string]any{
		"storage_type":            models.StorageTypeES,
		"storage_id":              storageClusterId,
		"db":                      indexSet,
		"measurement":             models.TSGroupDefaultMeasurement,
		"source_type":             sourceType,
		"options":                 options,
		"storage_cluster_records": clusterRecords,
		"data_label":              rt.DataLabel,
		"field_alias":             fieldAliasSettings, // 添加字段别名
	})
	if err != nil {
		return tableId, "", err
	}

	parts := strings.Split(tableId, ".")

	if len(parts) == 1 {
		// 如果长度为 1，补充 `.__default__`
		logger.Infof("composeEsTableIdDetail: table_id [%s] is missing '.', adding '.__default__'", tableId)
		tableId = fmt.Sprintf("%s.__default__", tableId)
	} else if len(parts) != 2 {
		// 如果长度不是 2，记录错误日志并返回
		err = errors.Errorf("invalid table_id format: too many dots in %q", tableId)
		logger.Errorf("composeEsTableIdDetail: table_id [%s] is invalid, contains too many dots", tableId)
		return tableId, "", err
	}
	// 大部份情况下,len(parts)=2，保持原样，无需显式处理

	logger.Infof("composeEsTableIdDetail:compose success, table_id [%s], detail [%s]", tableId, detailStr)
	return tableId, detailStr, err
}

func (s *SpacePusher) composeDorisTableIdDetail(tableId string, bkbaseTableId string, fieldAliasSettings map[string]string) (string, string, error) {
	logger.Infof("composeDorisTableIdDetail: table_id [%s], bkbase_table_id [%s]", tableId, bkbaseTableId)

	db := mysql.GetDBSession().DB

	var rt resulttable.ResultTable
	if err := resulttable.NewResultTableQuerySet(db).Select(resulttable.ResultTableDBSchema.DataLabel).TableIdEq(tableId).One(&rt); err != nil {
		return tableId, "", err
	}

	if fieldAliasSettings == nil {
		fieldAliasSettings = make(map[string]string)
	}

	// 组装数据
	detailStr, err := jsonx.MarshalString(map[string]any{
		"storage_type": models.StorageTypeBkSql,
		"db":           bkbaseTableId,
		"measurement":  models.DorisMeasurement,
		"data_label":   rt.DataLabel,
		"field_alias":  fieldAliasSettings, // 添加字段别名
	})
	if err != nil {
		return tableId, "", err
	}

	parts := strings.Split(tableId, ".")

	if len(parts) == 1 {
		// 如果长度为 1，补充 `.__default__`
		logger.Infof("composeDorisTableIdDetail: table_id [%s] is missing '.', adding '.__default__'", tableId)
		tableId = fmt.Sprintf("%s.__default__", tableId)
	} else if len(parts) != 2 {
		// 如果长度不是 2，记录错误日志并返回
		err = errors.Errorf("invalid table_id format: too many dots in %q", tableId)
		logger.Errorf("composeDorisTableIdDetail: table_id [%s] is invalid, contains too many dots", tableId)
		return tableId, "", err
	}
	// 大部份情况下,len(parts)=2，保持原样，无需显式处理

	logger.Infof("composeDorisTableIdDetail:compose success, table_id [%s], detail [%s]", tableId, detailStr)
	return tableId, detailStr, err
}

type InfluxdbTableData struct {
	InfluxdbProxyStorageId uint     `json:"influxdb_proxy_storage_id"`
	Database               string   `json:"database"`
	RealTableName          string   `json:"real_table_name"`
	TagsKey                []string `json:"tags_key"`
}

// 获取influxdb 和 vm的结果表
func (s *SpacePusher) getTableInfoForInfluxdbAndVm(bkTenantId string, tableIdList []string) (map[string]map[string]any, error) {
	logger.Debugf("start to push table_id detail data, table_id_list->[%s]", tableIdList)
	db := mysql.GetDBSession().DB

	var influxdbStorageList []storage.InfluxdbStorage
	if len(tableIdList) != 0 {
		// 如果结果表存在，则过滤指定的结果表
		for _, chunkTableIdList := range slicex.ChunkSlice(tableIdList, 0) {
			var tempList []storage.InfluxdbStorage
			if err := storage.NewInfluxdbStorageQuerySet(db).BkTenantIdEq(bkTenantId).TableIDIn(chunkTableIdList...).All(&tempList); err != nil {
				return nil, err
			}
			influxdbStorageList = append(influxdbStorageList, tempList...)
		}
	} else {
		if err := storage.NewInfluxdbStorageQuerySet(db).BkTenantIdEq(bkTenantId).All(&influxdbStorageList); err != nil {
			return nil, err
		}
	}

	influxdbTableMap := make(map[string]InfluxdbTableData)
	for _, i := range influxdbStorageList {
		tagsKey := make([]string, 0)
		if i.PartitionTag != "" {
			tagsKey = strings.Split(i.PartitionTag, ",")
		}
		influxdbTableMap[i.TableID] = InfluxdbTableData{
			InfluxdbProxyStorageId: i.InfluxdbProxyStorageId,
			Database:               i.Database,
			RealTableName:          i.RealTableName,
			TagsKey:                tagsKey,
		}
	}
	// 获取vm集群名信息
	var vmCLusterList []storage.ClusterInfo
	if err := storage.NewClusterInfoQuerySet(db).Select(storage.ClusterInfoDBSchema.ClusterID, storage.ClusterInfoDBSchema.ClusterName).ClusterTypeEq(models.StorageTypeVM).All(&vmCLusterList); err != nil {
		return nil, err
	}
	vmClusterIdNameMap := make(map[uint]string)
	for _, c := range vmCLusterList {
		vmClusterIdNameMap[c.ClusterID] = c.ClusterName
	}

	var vmRecordList []storage.AccessVMRecord
	if len(tableIdList) != 0 {
		// 如果结果表存在，则过滤指定的结果表
		for _, chunkTableIdList := range slicex.ChunkSlice(tableIdList, 0) {
			var tempList []storage.AccessVMRecord
			if err := storage.NewAccessVMRecordQuerySet(db).Select(storage.AccessVMRecordDBSchema.ResultTableId, storage.AccessVMRecordDBSchema.VmClusterId, storage.AccessVMRecordDBSchema.VmResultTableId).BkTenantIdEq(bkTenantId).ResultTableIdIn(chunkTableIdList...).All(&tempList); err != nil {
				return nil, err
			}
			vmRecordList = append(vmRecordList, tempList...)
		}
	} else {
		if err := storage.NewAccessVMRecordQuerySet(db).Select(storage.AccessVMRecordDBSchema.ResultTableId, storage.AccessVMRecordDBSchema.VmClusterId, storage.AccessVMRecordDBSchema.VmResultTableId).BkTenantIdEq(bkTenantId).All(&vmRecordList); err != nil {
			return nil, err
		}
	}
	vmTableMap := make(map[string]map[string]any)
	for _, record := range vmRecordList {
		vmTableMap[record.ResultTableId] = map[string]any{"vm_rt": record.VmResultTableId, "storage_name": vmClusterIdNameMap[record.VmClusterId], "storage_id": record.VmClusterId}
	}

	var rtCmdbLevelOptionList []resulttable.ResultTableOption
	if err := resulttable.NewResultTableOptionQuerySet(db).Select(resulttable.ResultTableOptionDBSchema.TableID, resulttable.ResultTableOptionDBSchema.Value).NameEq(models.CmdbLevelVmrt).All(&rtCmdbLevelOptionList); err != nil {
		logger.Errorf("getTableInfoForInfluxdbAndVm: get cmdb level vm rt option error:%s", err.Error())
	}

	cmdbLevelVmrtMap := make(map[string]string)
	for _, option := range rtCmdbLevelOptionList {
		cmdbLevelVmrtMap[option.TableID] = option.Value
	}

	// 获取proxy关联的集群信息
	var influxdbProxyStorageList []storage.InfluxdbProxyStorage
	if err := storage.NewInfluxdbProxyStorageQuerySet(db).Select(storage.InfluxdbProxyStorageDBSchema.ID, storage.InfluxdbProxyStorageDBSchema.ProxyClusterId, storage.InfluxdbProxyStorageDBSchema.InstanceClusterName).All(&influxdbProxyStorageList); err != nil {
		return nil, err
	}
	storageClusterMap := make(map[uint]storage.InfluxdbProxyStorage)
	for _, p := range influxdbProxyStorageList {
		storageClusterMap[p.ID] = p
	}

	tableIdInfo := make(map[string]map[string]any)

	for tableId, detail := range influxdbTableMap {
		storageCluster := storageClusterMap[detail.InfluxdbProxyStorageId]

		tableIdInfo[tableId] = map[string]any{
			"storage_id":   storageCluster.ProxyClusterId,
			"storage_name": "",
			"cluster_name": storageCluster.InstanceClusterName,
			"db":           detail.Database,
			"measurement":  detail.RealTableName,
			"vm_rt":        "",
			"tags_key":     detail.TagsKey,
			"storage_type": models.StorageTypeInfluxdb,
		}
	}

	// 处理 vm 的数据信息
	for tableId, detail := range vmTableMap {
		// 如果存在 cmdb_level_vm_rt 的 option，则添加到 detail 中
		if cmdbLevelVmrt, ok := cmdbLevelVmrtMap[tableId]; ok {
			logger.Infof("getTableInfoForInfluxdbAndVm: found cmdb_level_vm_rt for table_id %s, value: %s", tableId, cmdbLevelVmrt)
			detail["cmdb_level_vm_rt"] = cmdbLevelVmrt
		} else {
			detail["cmdb_level_vm_rt"] = ""
		}
		if _, ok := tableIdInfo[tableId]; ok {
			tableIdInfo[tableId]["vm_rt"] = detail["vm_rt"]
			tableIdInfo[tableId]["storage_name"] = detail["storage_name"]
			tableIdInfo[tableId]["storage_type"] = models.StorageTypeVM
			tableIdInfo[tableId]["cmdb_level_vm_rt"] = detail["cmdb_level_vm_rt"]
		} else {
			detail["cluster_name"] = ""
			detail["db"] = ""
			detail["measurement"] = ""
			detail["tags_key"] = []string{}
			tableIdInfo[tableId] = detail
			tableIdInfo[tableId]["storage_type"] = models.StorageTypeVM
		}
	}
	return tableIdInfo, nil
}

// 通过结果表Id, 获取对应的 option 配置, 通过 option 转到到 measurement 类型
func (s *SpacePusher) getMeasurementTypeByTableId(bkTenantId string, tableIdList []string, tableList []resulttable.ResultTable, tableDataIdMap map[string]uint) (map[string]string, error) {
	if len(tableIdList) == 0 {
		return make(map[string]string), nil
	}
	db := mysql.GetDBSession().DB
	// 过滤对应关系，用以进行判断单指标单表、多指标单表
	var rtoList []resulttable.ResultTableOption
	for _, chunkTableIdList := range slicex.ChunkSlice(tableIdList, 0) {
		var tempList []resulttable.ResultTableOption
		if err := resulttable.NewResultTableOptionQuerySet(db).Select(resulttable.ResultTableOptionDBSchema.TableID, resulttable.ResultTableOptionDBSchema.Value).BkTenantIdEq(bkTenantId).TableIDIn(chunkTableIdList...).NameEq(models.OptionIsSplitMeasurement).All(&tempList); err != nil {
			return nil, err
		}
		rtoList = append(rtoList, tempList...)
	}

	rtoMap := make(map[string]bool)
	for _, rto := range rtoList {
		var value bool
		if err := jsonx.UnmarshalString(rto.Value, &value); err != nil {
			return nil, err
		}
		rtoMap[rto.TableID] = value
	}

	var bkDataIdList []uint
	for _, bkDataId := range tableDataIdMap {
		bkDataIdList = append(bkDataIdList, bkDataId)
	}
	bkDataIdList = slicex.RemoveDuplicate(&bkDataIdList)
	// 过滤数据源对应的 etl_config
	dataIdEtlMap := make(map[uint]string)
	var dsList []resulttable.DataSource
	if len(bkDataIdList) != 0 {
		if err := resulttable.NewDataSourceQuerySet(db).Select(resulttable.DataSourceDBSchema.BkDataId, resulttable.DataSourceDBSchema.EtlConfig).BkTenantIdEq(bkTenantId).BkDataIdIn(bkDataIdList...).All(&dsList); err != nil {
			return nil, err
		}
	}
	for _, ds := range dsList {
		dataIdEtlMap[ds.BkDataId] = ds.EtlConfig
	}

	// 获取到对应的类型
	measurementTypeMap := make(map[string]string)
	tableIdCutterMap, err := NewResultTableSvc(nil).GetTableIdCutter(bkTenantId, tableIdList)
	if err != nil {
		return nil, err
	}
	for _, table := range tableList {
		bkDataId := tableDataIdMap[table.TableId]
		etlConfig := dataIdEtlMap[bkDataId]
		// 获取是否禁用指标切分模式
		isDisableMetricCutter := tableIdCutterMap[table.TableId]
		measurementTypeMap[table.TableId] = s.getMeasurementType(table.SchemaType, rtoMap[table.TableId], isDisableMetricCutter, etlConfig)
	}
	return measurementTypeMap, nil
}

// 获取表类型
func (s *SpacePusher) getMeasurementType(schemaType string, isSplitMeasurement, isDisableMetricCutter bool, etlConfig string) string {
	// - 当 schema_type 为 fixed 时，为多指标单表
	if schemaType == models.ResultTableSchemaTypeFixed {
		return models.MeasurementTypeBkTraditional
	}
	// - 当 schema_type 为 free 时，
	if schemaType == models.ResultTableSchemaTypeFree {
		// - 如果 is_split_measurement 为 True, 则为单指标单表
		if isSplitMeasurement {
			return models.MeasurementTypeBkSplit
		}
		// - is_split_measurement 为 False
		// - 如果etl_config 不为`bk_standard_v2_time_series`
		if etlConfig != models.ETLConfigTypeBkStandardV2TimeSeries {
			return models.MeasurementTypeBkExporter
		}
		// - etl_config 为`bk_standard_v2_time_series`，
		// - 如果 is_disable_metric_cutter 为 False，则为固定 metric_name，metric_value
		if !isDisableMetricCutter {
			return models.MeasurementTypeBkExporter
		}
		// - 否则为自定义多指标单表
		return models.MeasurementTypeBkStandardV2TimeSeries

	}
	return models.MeasurementTypeBkTraditional
}

// 组装结果表对应的指标数据
func (s *SpacePusher) composeTableIdFields(bkTenantId string, tableIds []string) (map[string][]string, error) {
	if len(tableIds) == 0 {
		return make(map[string][]string), nil
	}

	db := mysql.GetDBSession().DB

	// 分批配置
	queryConfig := GetDefaultRTFBatchConfig()

	// 分批查询 ResultTableField,降低DB压力
	var rtfList []resulttable.ResultTableField

	logger.Infof("composeTableIdFields: Starting batch query for ResultTableField records, target tables: %d, batch size: %d records per batch",
		len(tableIds), queryConfig.BatchSize)

	// 记录查询开始时间
	startTime := time.Now()
	offset := 0
	batchNum := 1
	totalRecords := 0

	for {
		logger.Infof("composeTableIdFields: Querying ResultTableField batch %d, offset: %d, limit: %d", batchNum, offset, queryConfig.BatchSize)

		// 执行当前批次的查询，使用 Limit 和 Offset 进行分页
		var batchRtfList []resulttable.ResultTableField
		query := resulttable.NewResultTableFieldQuerySet(db).
			Select(resulttable.ResultTableFieldDBSchema.TableID, resulttable.ResultTableFieldDBSchema.FieldName).
			TagEq(models.ResultTableFieldTagMetric).
			TableIDIn(tableIds...).
			BkTenantIdEq(bkTenantId).
			Limit(queryConfig.BatchSize).
			Offset(offset)

		if err := query.All(&batchRtfList); err != nil {
			logger.Errorf("composeTableIdFields: Failed to query ResultTableField batch %d (offset: %d): %v", batchNum, offset, err)
			return nil, err
		}

		// 如果当前批次没有数据，说明已经查询完毕
		if len(batchRtfList) == 0 {
			logger.Infof("composeTableIdFields: No more ResultTableField records found, batch query completed")
			break
		}

		// 合并当前批次的结果
		rtfList = append(rtfList, batchRtfList...)
		totalRecords += len(batchRtfList)

		logger.Infof("composeTableIdFields: Completed ResultTableField batch %d, retrieved %d records, total so far: %d",
			batchNum, len(batchRtfList), totalRecords)

		// 如果当前批次的记录数少于批次大小，说明这是最后一批
		if len(batchRtfList) < queryConfig.BatchSize {
			logger.Infof("composeTableIdFields: Last batch detected (records: %d < batch_size: %d), query completed", len(batchRtfList), queryConfig.BatchSize)
			break
		}

		// 准备下一批次
		offset += queryConfig.BatchSize
		batchNum++

		// 批次间延迟
		time.Sleep(queryConfig.BatchDelay)
	}

	queryDuration := time.Since(startTime)
	logger.Infof("composeTableIdFields:: ResultTableField batch query completed, total records: %d, batches: %d, duration: %v", totalRecords, batchNum, queryDuration)

	// ========== 构建 tableIdFieldMap（保持原逻辑&数据结构不变）==========
	tableIdFieldMap := make(map[string][]string)
	for _, field := range rtfList {
		if fieldList, ok := tableIdFieldMap[field.TableID]; ok {
			tableIdFieldMap[field.TableID] = append(fieldList, field.FieldName)
		} else {
			tableIdFieldMap[field.TableID] = []string{field.FieldName}
		}
	}

	// 根据 option 过滤是否有开启黑名单，如果开启黑名单，则指标会有过期时间
	var rtoList []resulttable.ResultTableOption
	if err := resulttable.NewResultTableOptionQuerySet(db).
		Select(resulttable.ResultTableOptionDBSchema.TableID).
		BkTenantIdEq(bkTenantId).
		TableIDIn(tableIds...).
		NameEq(models.OptionEnableFieldBlackList).
		ValueEq("false").
		All(&rtoList); err != nil {
		return nil, err
	}

	var whiteTableIdList []string
	for _, o := range rtoList {
		whiteTableIdList = append(whiteTableIdList, o.TableID)
	}
	whiteTableIdList = slicex.RemoveDuplicate(&whiteTableIdList)
	// 剩余的结果表，需要判断是否时序的，然后根据过期时间过滤数据

	logger.Infof("white table_id list: %v", whiteTableIdList)

	tableIdList := slicex.StringSet2List(slicex.StringList2Set(tableIds).Difference(slicex.StringList2Set(whiteTableIdList)))
	if len(tableIdList) == 0 {
		return make(map[string][]string), nil
	}

	tsInfo, err := s.filterTsInfo(bkTenantId, tableIdList)
	if err != nil {
		return nil, err
	}
	// 组装结果表对应的指标数据
	tableIdMetrics := make(map[string][]string)
	existTableIdList := make(map[string]bool)

	// 如果是自定义指标，优先使用自定义指标的字段信息
	if tsInfo != nil {
		for tableId, groupId := range tsInfo.TableIdTsGroupIdMap {
			if metrics, ok := tsInfo.GroupIdFieldsMap[groupId]; ok {
				tableIdMetrics[tableId] = metrics
			} else {
				tableIdMetrics[tableId] = []string{}
			}
			existTableIdList[tableId] = true
		}
	}

	// 处理非自定义指标
	for tableId, fieldList := range tableIdFieldMap {
		if !existTableIdList[tableId] {
			tableIdMetrics[tableId] = fieldList
		}
	}

	return tableIdMetrics, nil
}

type TsInfo struct {
	TableIdTsGroupIdMap map[string]uint
	GroupIdFieldsMap    map[uint][]string
}

// 根据结果表获取对应的时序数据
func (s *SpacePusher) filterTsInfo(bkTenantId string, tableIds []string) (*TsInfo, error) {
	if len(tableIds) == 0 {
		return nil, nil
	}
	db := mysql.GetDBSession().DB
	var tsGroupList []customreport.TimeSeriesGroup
	if err := customreport.NewTimeSeriesGroupQuerySet(db).BkTenantIdEq(bkTenantId).TableIDIn(tableIds...).All(&tsGroupList); err != nil {
		return nil, err
	}
	if len(tsGroupList) == 0 {
		return nil, nil
	}

	var tsGroupIdList []uint
	TableIdTsGroupIdMap := make(map[string]uint)
	var tsGroupTableId []string
	for _, group := range tsGroupList {
		tsGroupIdList = append(tsGroupIdList, group.TimeSeriesGroupID)
		TableIdTsGroupIdMap[group.TableID] = group.TimeSeriesGroupID
		tsGroupTableId = append(tsGroupTableId, group.TableID)
	}

	// NOTE: 针对自定义时序，过滤掉历史废弃的指标
	// 根据特性开关决定过滤方式:
	// 1. 启用 is_active 字段时: 只查询 is_active=true 的指标
	// 2. 使用原有方式时: 查询时间在 TIME_SERIES_METRIC_EXPIRED_SECONDS 内的指标
	beginTime := time.Now().UTC().Add(-time.Duration(cfg.GlobalTimeSeriesMetricExpiredSeconds) * time.Second)

	// 分批查询 TimeSeriesMetric（优化部分
	var tsmList []customreport.TimeSeriesMetric

	if len(tsGroupIdList) != 0 {
		// 分批查询配置
		queryConfig := GetDefaultRTFBatchConfig()

		filterMode := "last_modify_time"
		if cfg.GlobalEnableTsMetricFilterByIsActive {
			filterMode = "is_active"
		}

		logger.Infof("filterTsInfo: Starting batch query for TimeSeriesMetric records, target groups: %d, batch size: %d records per batch, filter_mode: %s",
			len(tsGroupIdList), queryConfig.BatchSize, filterMode)

		// 记录查询开始时间
		startTime := time.Now()
		offset := 0
		batchNum := 1
		totalRecords := 0

		for {
			logger.Infof("filterTsInfo: Querying TimeSeriesMetric batch %d, offset: %d, limit: %d",
				batchNum, offset, queryConfig.BatchSize)

			// 执行当前批次的查询，使用 Limit 和 Offset 进行分页
			var batchTsmList []customreport.TimeSeriesMetric
			query := customreport.NewTimeSeriesMetricQuerySet(db).
				Select(customreport.TimeSeriesMetricDBSchema.FieldName, customreport.TimeSeriesMetricDBSchema.GroupID).
				GroupIDIn(tsGroupIdList...).
				Limit(queryConfig.BatchSize).
				Offset(offset)

			// 根据特性开关添加不同的过滤条件
			if cfg.GlobalEnableTsMetricFilterByIsActive {
				// 启用 is_active 字段过滤: 只查询活跃的指标
				query = query.IsActiveEq(true)
			} else {
				// 使用原有方式: 根据最后修改时间过滤
				query = query.LastModifyTimeGte(beginTime)
			}

			if err := query.All(&batchTsmList); err != nil {
				logger.Errorf("filterTsInfo: Failed to query TimeSeriesMetric batch %d (offset: %d): %v", batchNum, offset, err)
				return nil, err
			}

			// 如果当前批次没有数据，说明已经查询完毕
			if len(batchTsmList) == 0 {
				logger.Infof("filterTsInfo: No more TimeSeriesMetric records found, batch query completed")
				break
			}

			// 合并当前批次的结果
			tsmList = append(tsmList, batchTsmList...)
			totalRecords += len(batchTsmList)

			logger.Infof("filterTsInfo: Completed TimeSeriesMetric batch %d, retrieved %d records, total so far: %d",
				batchNum, len(batchTsmList), totalRecords)

			// 如果当前批次的记录数少于批次大小，说明这是最后一批
			if len(batchTsmList) < queryConfig.BatchSize {
				logger.Infof("filterTsInfo: Last batch detected (records: %d < batch_size: %d), query completed",
					len(batchTsmList), queryConfig.BatchSize)
				break
			}

			// 准备下一批次
			offset += queryConfig.BatchSize
			batchNum++

			// 批次间延迟
			time.Sleep(queryConfig.BatchDelay)
		}

		queryDuration := time.Since(startTime)
		logger.Infof("filterTsInfo: TimeSeriesMetric batch query completed, total records: %d, batches: %d, duration: %v",
			totalRecords, batchNum, queryDuration)
	}

	groupIdFieldsMap := make(map[uint][]string)
	for _, metric := range tsmList {
		if fieldList, ok := groupIdFieldsMap[metric.GroupID]; ok {
			groupIdFieldsMap[metric.GroupID] = append(fieldList, metric.FieldName)
		} else {
			groupIdFieldsMap[metric.GroupID] = []string{metric.FieldName}
		}
	}

	return &TsInfo{
		TableIdTsGroupIdMap: TableIdTsGroupIdMap,
		GroupIdFieldsMap:    groupIdFieldsMap,
	}, nil
}

// 获取结果表对应的集群 ID
func (s *SpacePusher) getTableIdClusterId(bkTenantId string, tableIds []string) (map[string]string, error) {
	if len(tableIds) == 0 {
		return make(map[string]string), nil
	}
	db := mysql.GetDBSession().DB
	var dsrtList []resulttable.DataSourceResultTable
	if err := resulttable.NewDataSourceResultTableQuerySet(db).Select(resulttable.DataSourceResultTableDBSchema.BkDataId, resulttable.DataSourceResultTableDBSchema.TableId).BkTenantIdEq(bkTenantId).TableIdIn(tableIds...).All(&dsrtList); err != nil {
		return nil, err
	}
	if len(dsrtList) == 0 {
		return make(map[string]string), nil
	}
	var dataIds []uint
	for _, dsrt := range dsrtList {
		dataIds = append(dataIds, dsrt.BkDataId)
	}
	// 过滤到集群的数据源，仅包含两类，集群内置和集群自定义，已删除状态但是允许访问历史数据的集群依然进行推送
	qs := bcs.NewBCSClusterInfoQuerySet(db)

	dataIds = slicex.RemoveDuplicate(&dataIds)
	var clusterListA []bcs.BCSClusterInfo
	if err := qs.Select(bcs.BCSClusterInfoDBSchema.K8sMetricDataID, bcs.BCSClusterInfoDBSchema.ClusterID).BkTenantIdEq(bkTenantId).K8sMetricDataIDIn(dataIds...).All(&clusterListA); err != nil {
		return nil, err
	}

	var clusterListB []bcs.BCSClusterInfo
	if err := qs.Select(bcs.BCSClusterInfoDBSchema.CustomMetricDataID, bcs.BCSClusterInfoDBSchema.ClusterID).BkTenantIdEq(bkTenantId).CustomMetricDataIDIn(dataIds...).All(&clusterListB); err != nil {
		return nil, err
	}

	dataIdClusterIdMap := make(map[uint]string)
	for _, c := range clusterListA {
		dataIdClusterIdMap[c.K8sMetricDataID] = c.ClusterID
	}
	for _, c := range clusterListB {
		dataIdClusterIdMap[c.CustomMetricDataID] = c.ClusterID
	}
	// 组装结果表到集群的信息
	tableIdClusterIdMap := make(map[string]string)
	for _, dsrt := range dsrtList {
		tableIdClusterIdMap[dsrt.TableId] = dataIdClusterIdMap[dsrt.BkDataId]
	}
	return tableIdClusterIdMap, nil
}

// PushBkAppToSpace  推送 bk_app_code 对应的 space 关联
func (s *SpacePusher) PushBkAppToSpace() (err error) {
	var appSpaces space.BkAppSpaces

	defer func() {
		if err != nil {
			logger.Errorf("PushBkAppToSpace error: %s", err.Error())
			return
		}

		logger.Infof("PushBkAppToSpace success")
	}()

	db := mysql.GetDBSession().DB
	if db == nil {
		return err
	}

	res := db.Find(&appSpaces)
	if res.Error != nil {
		err = res.Error
		return err
	}

	client := redis.GetStorageRedisInstance()
	key := cfg.BkAppToSpaceKey
	for field, value := range appSpaces.HashData() {
		// 多租户模式下，需要加上租户ID后缀
		if cfg.EnableMultiTenantMode {
			newValue := make([]string, 0)
			for _, spaceUID := range value {

				// 如果 spaceUID 为 *，则表示所有空间
				if spaceUID == "*" {
					newValue = append(newValue, spaceUID)
					continue
				}

				bkTenantId, err := tenant.GetTenantIdBySpaceUID(spaceUID)
				if err != nil {
					logger.Errorf("PushBkAppToSpace:get tenant id by space uid failed, space_uid [%s], err: %s", spaceUID, err)
					return err
				}
				newValue = append(newValue, fmt.Sprintf("%s|%s", spaceUID, bkTenantId))
			}
			value = newValue
		}

		valueStr, jsonErr := jsonx.MarshalString(value)
		if err != nil {
			logger.Errorf("%+v jsonMarshalString error %s", value, jsonErr)
			continue
		}
		_, err = client.HSetWithCompareAndPublish(key, field, valueStr, cfg.BkAppToSpaceChannelKey, field)
		if err != nil {
			return err
		}
	}

	return err
}

// PushSpaceTableIds 推送空间及对应的结果表和过滤条件
func (s *SpacePusher) PushSpaceTableIds(bkTenantId, spaceType, spaceId string) error {
	// NOTE:该操作比较特殊，Publish操作需要在这里进行而不能直接在HSetWithCompareAndPublish中进行

	isSuccess := false
	var err error
	logger.Infof("PushSpaceTableIds:start to push space table_id data, space_type [%s], space_id [%s]", spaceType, spaceId)
	// NOTE:这里统一根据Redis中的新老值是否存在差异决定是否需要Publish
	switch spaceType {
	case models.SpaceTypeBKCC:
		isSuccess, err = s.pushBkccSpaceTableIds(bkTenantId, spaceType, spaceId, nil)
		logger.Infof("PushSpaceTableIds:push bkcc space table_id data success, space_type [%s], space_id [%s]", spaceType, spaceId)
		if err != nil {
			logger.Errorf("PushSpaceTableIds:push bkcc space table_id data failed, space_type [%s], space_id [%s], err: %v", spaceType, spaceId, err)
			return err
		}
	case models.SpaceTypeBKCI:
		// 开启容器服务，则需要处理集群+业务+构建机+其它(在当前空间下创建的插件、自定义上报等)
		isSuccess, err = s.pushBkciSpaceTableIds(bkTenantId, spaceType, spaceId)
		logger.Infof("PushSpaceTableIds:push bkci space table_id data success, space_type [%s], space_id [%s]", spaceType, spaceId)
		if err != nil {
			logger.Errorf("PushSpaceTableIds:push bkci space table_id data failed, space_type [%s], space_id [%s], err: %v", spaceType, spaceId, err)
			return err
		}
	case models.SpaceTypeBKSAAS:
		isSuccess, err = s.pushBksaasSpaceTableIds(bkTenantId, spaceType, spaceId, nil)
		logger.Infof("PushSpaceTableIds:push bksaas space table_id data success, space_type [%s], space_id [%s]", spaceType, spaceId)
		if err != nil {
			logger.Errorf("PushSpaceTableIds:push bksaas space table_id data failed, space_type [%s], space_id [%s], err: %v", spaceType, spaceId, err)
			return err
		}
	default:
		logger.Errorf("PushSpaceTableIds:push space table_id data failed, space_type [%s], space_id [%s], err: %v", spaceType, spaceId, err)
		return nil
	}
	logger.Infof("PushSpaceTableIds:push space table_id data successfully, space_type [%s], space_id [%s],is_success [%v]", spaceType, spaceId, isSuccess)

	return nil
}

// composeValue 组装数据
func (s *SpacePusher) composeValue(values *map[string]map[string]any, composedData *map[string]map[string]any) {
	if composedData != nil && len(*composedData) != 0 {
		for tid, val := range *composedData {
			(*values)[tid] = val
		}
	}
}

// 推送 bkcc 类型空间数据
func (s *SpacePusher) pushBkccSpaceTableIds(bkTenantId, spaceType, spaceId string, options *optionx.Options) (bool, error) {
	if options == nil {
		options = optionx.NewOptions(nil)
	}
	logger.Infof("pushBkccSpaceTableIds:start to push bkcc space table_id, space_type [%s], space_id [%s]", spaceType, spaceId)
	// 组装基础数据,需要filters
	values, errMetric := s.composeData(bkTenantId, spaceType, spaceId, nil, nil, options)
	if errMetric != nil {
		logger.Errorf("pushBkccSpaceTableIds:compose space table_id data failed, space_type [%s], space_id [%s], err: %s", spaceType, spaceId, errMetric)
	}
	// 如果为空，则初始化一次
	if values == nil {
		values = make(map[string]map[string]any)
	}

	// 添加预计算结果表,不需要filters
	recordRuleValues, errRecordRule := s.composeRecordRuleTableIds(spaceType, spaceId)
	if errRecordRule != nil {
		logger.Errorf("pushBkccSpaceTableIds:compose record rule table_id data failed, space_type [%s], space_id [%s], err: %s", spaceType, spaceId, errRecordRule)
	}
	s.composeValue(&values, &recordRuleValues)

	// 追加es空间路由表,不需要filters
	esValues, errEs := s.ComposeEsTableIds(spaceType, spaceId)
	if errEs != nil {
		logger.Errorf("pushBkccSpaceTableIds:compose es space table_id data failed, space_type [%s], space_id [%s], err: %s", spaceType, spaceId, errEs)
	}
	s.composeValue(&values, &esValues)

	// 追加Doris空间路由表,不需要filters
	dorisValues, errDoris := s.ComposeDorisTableIds(spaceType, spaceId)
	if errDoris != nil {
		logger.Errorf("pushBkccSpaceTableIds:compose doris space table_id data failed, space_type [%s], space_id [%s], err: %s", spaceType, spaceId, errDoris)
	}
	s.composeValue(&values, &dorisValues)

	// 追加关联的BKCI相关的ES结果表,不需要filters
	esBkciValues, errEsBkci := s.ComposeRelatedBkciTableIds(spaceType, spaceId)
	if errEsBkci != nil {
		logger.Warnf("pushBkccSpaceTableIds:compose es bkci space table_id data failed, space_type [%s], space_id [%s], err: %s", spaceType, spaceId, errEsBkci)
	}
	logger.Infof("pushBkccSpaceTableIds:compose es bkci space table_id data successfully, space_type [%s], space_id [%s],data->[%v]", spaceType, spaceId, esBkciValues)
	s.composeValue(&values, &esBkciValues)

	// 如果有异常，则直接返回
	if errMetric != nil && errEs != nil && errRecordRule != nil {
		return false, errors.Wrapf(errEs, "pushBkccSpaceTableIds:compose space table_id data failed, space_type [%s], space_id [%s], err: %s", spaceType, spaceId, errMetric)
	}
	if len(values) != 0 {
		var redisKey string

		// 如果开启了多租户模式，则需要加上租户ID后缀
		if cfg.EnableMultiTenantMode {
			redisKey = fmt.Sprintf("%s__%s|%s", spaceType, spaceId, bkTenantId)
		} else {
			redisKey = fmt.Sprintf("%s__%s", spaceType, spaceId)
		}

		client := redis.GetStorageRedisInstance()
		valuesStr, err := jsonx.MarshalString(values)
		if err != nil {
			return false, errors.Wrapf(err, "pushBkccSpaceTableIds:push bkcc space [%s] marshal valued [%v] failed", redisKey, values)
		}
		// TODO: 待旁路没有问题，可以移除的逻辑
		key := cfg.SpaceToResultTableKey
		logger.Infof("pushBkccSpaceTableIds:push_and_publish_space_router_info, key [%s], redisKey [%s], values [%v]", key, redisKey, valuesStr)

		channelName := fmt.Sprintf("%s__%s", spaceType, spaceId)
		// NOTE:这里的HSetWithCompareAndPublish会判定新老值是否存在差异，若存在差异，则进行Set & Publish
		logger.Infof("pushBkccSpaceTableIds:start to push_and_publish_space_router_info, key [%s], redisKey [%s], values [%v], channelName [%s], channelKey [%s]", key, redisKey, valuesStr, cfg.SpaceToResultTableChannel, channelName)
		isSuccess, err := client.HSetWithCompareAndPublish(key, redisKey, valuesStr, cfg.SpaceToResultTableChannel, channelName)
		if err != nil {
			logger.Errorf("pushBkccSpaceTableIds:failed to push_and_publish_space_router_info, key [%s], redisKey [%s], values [%v]", key, redisKey, valuesStr)
			return false, errors.Wrapf(err, "pushBkccSpaceTableIds:push bkcc space [%s] value [%v] failed", redisKey, valuesStr)
		}
		logger.Infof("pushBkccSpaceTableIds:push_and_publish_space_router_info, key [%s], redisKey [%s], values [%v], isSuccess [%t]", key, redisKey, valuesStr, isSuccess)
		return isSuccess, nil
	}
	logger.Infof("pushBkccSpaceTableIds:push redis space_to_result_table, space_type [%s], space_id [%s] success", spaceType, spaceId)
	return false, nil
}

// 推送 bcs 类型空间下的关联业务的数据
func (s *SpacePusher) pushBkciSpaceTableIds(bkTenantId, spaceType, spaceId string) (bool, error) {
	logger.Infof("pushBkciSpaceTableIds： start to push biz of bcs space table_id, space_type [%s], space_id [%s]", spaceType, spaceId)
	values, err := s.composeBcsSpaceBizTableIds(spaceType, spaceId)
	if err != nil {
		logger.Errorf("pushBkciSpaceTableIds： compose bcs space biz table_id data failed, space_type [%s], space_id [%s], err: %s", spaceType, spaceId, err)
	}
	// 处理为空的情况
	if values == nil {
		values = make(map[string]map[string]any)
	}
	// 追加 bcs 集群结果表
	bcsValues, err := s.composeBcsSpaceClusterTableIds(spaceType, spaceId)
	logger.Errorf("bcs values %v", bcsValues)
	if err != nil {
		logger.Errorf("pushBkciSpaceTableIds： compose bcs space cluster table_id data failed, space_type [%s], space_id [%s], err: %s", spaceType, spaceId, err)
	}
	s.composeValue(&values, &bcsValues)

	// 追加 bkci 空间级别的结果表
	bkciLevelValues, err := s.composeBkciLevelTableIds(bkTenantId, spaceType, spaceId)
	if err != nil {
		logger.Errorf("pushBkciSpaceTableIds： compose bcs space bkci level table_id data failed, space_type [%s], space_id [%s], err: %s", spaceType, spaceId, err)
	}
	s.composeValue(&values, &bkciLevelValues)
	// 追加剩余的结果表
	bkciOtherValues, err := s.composeBkciOtherTableIds(bkTenantId, spaceType, spaceId)
	if err != nil {
		logger.Errorf("pushBkciSpaceTableIds： compose bcs space bkci other table_id data failed, space_type [%s], space_id [%s], err: %s", spaceType, spaceId, err)
	}
	s.composeValue(&values, &bkciOtherValues)

	// 追加跨空间的结果表
	bkciCrossValues, err := s.composeBkciCrossTableIds(bkTenantId, spaceType, spaceId)
	if err != nil {
		logger.Errorf("pushBkciSpaceTableIds： compose bcs space bkci cross table_id data failed, space_type [%s], space_id [%s], err: %s")
	}
	s.composeValue(&values, &bkciCrossValues)
	// 追加全空间空间的结果表
	allTypeTableIdValues, err := s.composeAllTypeTableIds(spaceType, spaceId)
	if err != nil {
		logger.Errorf("pushBkciSpaceTableIds： compose all type table_id data failed, space_type [%s], space_id [%s], err: %s", spaceType, spaceId, err)
	}
	s.composeValue(&values, &allTypeTableIdValues)

	// 追加预计算路由
	recordRuleValues, errRecordRule := s.composeRecordRuleTableIds(spaceType, spaceId)
	if errRecordRule != nil {
		logger.Errorf("pushBkciSpaceTableIds： compose record rule table_id data failed, space_type [%s], space_id [%s], err: %s", spaceType, spaceId, errRecordRule)
	}
	s.composeValue(&values, &recordRuleValues)
	// 追加es空间结果表
	esValues, err := s.ComposeEsTableIds(spaceType, spaceId)
	if err != nil {
		logger.Errorf("pushBkciSpaceTableIds：compose es space table_id data failed, space_type [%s], space_id [%s], err: %s", spaceType, spaceId, err)
	}
	s.composeValue(&values, &esValues)

	// 追加Doris空间结果表
	dorisValues, err := s.ComposeDorisTableIds(spaceType, spaceId)
	if err != nil {
		logger.Errorf("pushBkciSpaceTableIds：compose doris space table_id data failed, space_type [%s], space_id [%s], err: %s", spaceType, spaceId, err)
	}
	s.composeValue(&values, &dorisValues)

	// 追加APM全局结果表
	apmAllTypeValues, errApmAllType := s.composeApmAllTypeTableIds(spaceType, spaceId)
	if errApmAllType != nil {
		logger.Errorf("pushBkciSpaceTableIds: compose apm all type space table_id data failed, space_type [%s], space_id [%s], err: %s", spaceType, spaceId, errApmAllType)
	}
	logger.Infof("pushBkciSpaceTableIds: compose apm all type space table_id data successfully, space_type [%s], space_id [%s],data->[%v]", spaceType, spaceId, apmAllTypeValues)
	s.composeValue(&values, &apmAllTypeValues)

	// 推送数据
	if len(values) != 0 {
		client := redis.GetStorageRedisInstance()

		var redisKey string
		// 如果开启了多租户模式，则需要加上租户ID后缀
		if cfg.EnableMultiTenantMode {
			redisKey = fmt.Sprintf("%s__%s|%s", spaceType, spaceId, bkTenantId)
		} else {
			redisKey = fmt.Sprintf("%s__%s", spaceType, spaceId)
		}

		valuesStr, err := jsonx.MarshalString(values)
		if err != nil {
			return false, errors.Wrapf(err, "push bkci space [%s] marshal valued failed", redisKey)
		}
		// TODO: 待旁路没有问题，可以移除的逻辑
		key := cfg.SpaceToResultTableKey
		channelName := fmt.Sprintf("%s__%s", spaceType, spaceId)
		// NOTE:这里的HSetWithCompareAndPublish会判定新老值是否存在差异，若存在差异，则进行Set & Publish
		logger.Infof("pushBkciSpaceTableIds:start to push_and_publish_space_router_info, key [%s], redisKey [%s], values [%v], channelName [%s], channelKey [%s]", key, redisKey, valuesStr, cfg.SpaceToResultTableChannel, channelName)
		isSuccess, err := client.HSetWithCompareAndPublish(key, redisKey, valuesStr, cfg.SpaceToResultTableChannel, channelName)
		if err != nil {
			logger.Errorf("pushBkciSpaceTableIds: push bkci space [%s] value [%v] failed, err: %s", redisKey, valuesStr, err)
			return false, errors.Wrapf(err, "push bkci space [%s] value [%v] failed", redisKey, valuesStr)
		}
		logger.Infof("push and publish redis space_to_result_table, space_type [%s], space_id [%s] isSuccess [%v]", spaceType, spaceId, isSuccess)
		return isSuccess, nil
	}
	logger.Infof("push redis space_to_result_table, space_type [%s], space_id [%s] success", spaceType, spaceId)
	return false, nil
}

// 推送 bksaas 类型空间下的数据
func (s *SpacePusher) pushBksaasSpaceTableIds(bkTenantId, spaceType, spaceId string, tableIdList []string) (bool, error) {
	logger.Infof("pushBksaasSpaceTableIds: start to push bksaas space table_id, space_type [%s], space_id [%s]", spaceType, spaceId)
	values, err := s.composeBksaasSpaceClusterTableIds(spaceType, spaceId, tableIdList)
	if err != nil {
		// 仅记录，不返回
		logger.Errorf("pushBksaasSpaceTableIds: pushBksaasSpaceTableIds error, compose bksaas space: [%s__%s] error: %s", spaceType, spaceId, err)
	}
	logger.Infof("pushBksaasSpaceTableIds: pushBksaasSpaceTableIds values: %v", values)
	if values == nil {
		values = make(map[string]map[string]any)
	}
	bksaasOtherValues, errOther := s.composeBksaasOtherTableIds(bkTenantId, spaceType, spaceId, tableIdList)
	if errOther != nil {
		logger.Errorf("pushBksaasSpaceTableIds: compose bksaas space other table_id data failed, space_type [%s], space_id [%s], err: %s", spaceType, spaceId, errOther)
	}
	s.composeValue(&values, &bksaasOtherValues)
	// 追加预计算空间路由
	recordRuleValues, errRecordRule := s.composeRecordRuleTableIds(spaceType, spaceId)
	if errRecordRule != nil {
		logger.Errorf("pushBksaasSpaceTableIds: compose record rule table_id data failed, space_type [%s], space_id [%s], err: %s", spaceType, spaceId, errRecordRule)
	}
	s.composeValue(&values, &recordRuleValues)
	// 追加es空间路由表
	esValues, esErr := s.ComposeEsTableIds(spaceType, spaceId)
	if esErr != nil {
		logger.Errorf("pushBksaasSpaceTableIds: compose es space table_id data failed, space_type [%s], space_id [%s], err: %s", spaceType, spaceId, esErr)
	}
	s.composeValue(&values, &esValues)

	// 追加Doris空间路由表
	dorisValues, errDoris := s.ComposeDorisTableIds(spaceType, spaceId)
	if errDoris != nil {
		logger.Errorf("pushBksaasSpaceTableIds: compose doris space table_id data failed, space_type [%s], space_id [%s], err: %s", spaceType, spaceId, errDoris)
	}
	s.composeValue(&values, &dorisValues)

	allTypeTableIdValues, allTypeErr := s.composeAllTypeTableIds(spaceType, spaceId)
	if allTypeErr != nil {
		logger.Errorf("pushBksaasSpaceTableIds: compose all type table_id data failed, space_type [%s], space_id [%s], err: %s", spaceType, spaceId, allTypeErr)
	}
	s.composeValue(&values, &allTypeTableIdValues)

	// 追加APM全局结果表
	apmAllTypeValues, errApmAllType := s.composeApmAllTypeTableIds(spaceType, spaceId)
	if errApmAllType != nil {
		logger.Errorf("pushBksaasSpaceTableIds:compose apm all type space table_id data failed, space_type [%s], space_id [%s], err: %s", spaceType, spaceId, errApmAllType)
	}
	logger.Infof("pushBksaasSpaceTableIds:compose apm all type space table_id data successfully, space_type [%s], space_id [%s],data->[%v]", spaceType, spaceId, apmAllTypeValues)
	s.composeValue(&values, &apmAllTypeValues)

	// 推送数据
	if len(values) != 0 {
		client := redis.GetStorageRedisInstance()

		var redisKey string
		// 如果开启了多租户模式，则需要加上租户ID后缀
		if cfg.EnableMultiTenantMode {
			redisKey = fmt.Sprintf("%s__%s|%s", spaceType, spaceId, bkTenantId)
		} else {
			redisKey = fmt.Sprintf("%s__%s", spaceType, spaceId)
		}

		valuesStr, err := jsonx.MarshalString(values)
		if err != nil {
			return false, errors.Wrapf(err, "push bksaas space [%s] marshal valued [%v] failed", redisKey, values)
		}
		// TODO: 待旁路没有问题，可以移除的逻辑
		key := cfg.SpaceToResultTableKey
		channelName := fmt.Sprintf("%s__%s", spaceType, spaceId)
		// NOTE:这里的HSetWithCompareAndPublish会判定新老值是否存在差异，若存在差异，则进行Set & Publish
		logger.Infof("pushBksaasSpaceTableIds: start to push_and_publish_space_router_info, key [%s], redisKey [%s], values [%v], channelName [%s], channelKey [%s]", key, redisKey, valuesStr, cfg.SpaceToResultTableChannel, channelName)
		isSuccess, err := client.HSetWithCompareAndPublish(key, redisKey, valuesStr, cfg.SpaceToResultTableChannel, channelName)
		if err != nil {
			logger.Errorf("pushBksaasSpaceTableIds: push bksaas space [%s] value [%v] failed", redisKey, valuesStr)
			return false, errors.Wrapf(err, "push bksaas space [%s] value [%v] failed", redisKey, valuesStr)
		}
		logger.Infof("pushBksaasSpaceTableIds: push and publish redis space_to_result_table, space_type [%s], space_id [%s] isSuccess [%v]", spaceType, spaceId, isSuccess)
		return isSuccess, nil

	}
	logger.Infof("pushBksaasSpaceTableIds: push redis space_to_result_table, space_type [%s], space_id [%s]", spaceType, spaceId)
	return false, nil
}

// composeRecordRuleTableIds compose record rule table ids 预计算结果表
func (s *SpacePusher) composeRecordRuleTableIds(spaceType, spaceId string) (map[string]map[string]any, error) {
	logger.Infof("start to push record rule table_id, space_type [%s], space_id [%s]", spaceType, spaceId)
	db := mysql.GetDBSession().DB
	var recordRuleList []recordrule.RecordRule
	if err := recordrule.NewRecordRuleQuerySet(db).Select(recordrule.RecordRuleDBSchema.TableId).SpaceTypeEq(spaceType).SpaceIdEq(spaceId).All(&recordRuleList); err != nil {
		return nil, err
	}
	// 组装数据
	dataValues := make(map[string]map[string]any)
	for _, recordRuleObj := range recordRuleList {
		// dataValues[recordRuleObj.TableId] = map[string]interface{}{"filters": []interface{}{}}
		options := FilterBuildContext{
			SpaceType: spaceType,
			SpaceId:   spaceId,
			TableId:   recordRuleObj.TableId,
		}
		filters := s.buildFiltersByUsage(options, UsageComposeRecordRuleTableIds)
		dataValues[recordRuleObj.TableId] = map[string]any{"filters": filters}
	}
	// 二段式校验&补充
	dataValuesToRedis := make(map[string]map[string]any)
	for tid, values := range dataValues {
		reformattedTid := reformatTableId(tid)
		dataValuesToRedis[reformattedTid] = values
	}
	return dataValuesToRedis, nil
}

// ComposeEsTableIds 组装关联的ES结果表
func (s *SpacePusher) ComposeEsTableIds(spaceType, spaceId string) (map[string]map[string]any, error) {
	logger.Infof("start to push es table_id, space_type [%s], space_id [%s]", spaceType, spaceId)
	bizId, err := s.getBizIdBySpace(spaceType, spaceId)
	if err != nil {
		return nil, errors.Wrapf(err, "compose es table_id, get biz_id by space failed, space_type [%s], space_id [%s]", spaceType, spaceId)
	}
	db := mysql.GetDBSession().DB
	var rtList []resulttable.ResultTable
	if err := resulttable.NewResultTableQuerySet(db).Select(resulttable.ResultTableDBSchema.TableId).BkBizIdEq(bizId).DefaultStorageEq(models.StorageTypeES).IsDeletedEq(false).IsEnableEq(true).All(&rtList); err != nil {
		return nil, err
	}
	dataValues := make(map[string]map[string]any)
	for _, rt := range rtList {
		// dataValues[rt.TableId] = map[string]interface{}{"filters": []interface{}{}}
		options := FilterBuildContext{
			SpaceType: spaceType,
			SpaceId:   spaceId,
			TableId:   rt.TableId,
		}
		// 使用统一抽象方法生成filters
		filters := s.buildFiltersByUsage(options, UsageComposeEsTableIds)
		dataValues[rt.TableId] = map[string]any{"filters": filters}
	}

	// 二段式校验&补充
	dataValuesToRedis := make(map[string]map[string]any)
	for tid, values := range dataValues {
		reformattedTid := reformatTableId(tid)
		dataValuesToRedis[reformattedTid] = values
	}

	return dataValuesToRedis, nil
}

// ComposeDorisTableIds 组装关联的Doris结果表
func (s *SpacePusher) ComposeDorisTableIds(spaceType, spaceId string) (map[string]map[string]any, error) {
	logger.Infof("ComposeDorisTableIds: start to push doris table_id, space_type [%s], space_id [%s]", spaceType, spaceId)
	bizId, err := s.getBizIdBySpace(spaceType, spaceId)
	if err != nil {
		return nil, errors.Wrapf(err, "ComposeDorisTableIds: compose doris table_id, get biz_id by space failed, space_type [%s], space_id [%s]", spaceType, spaceId)
	}

	db := mysql.GetDBSession().DB
	var rtList []resulttable.ResultTable
	if err := resulttable.NewResultTableQuerySet(db).Select(resulttable.ResultTableDBSchema.TableId).BkBizIdEq(bizId).DefaultStorageEq(models.StorageTypeDoris).IsDeletedEq(false).IsEnableEq(true).All(&rtList); err != nil {
		return nil, err
	}
	dataValues := make(map[string]map[string]any)
	for _, rt := range rtList {
		// dataValues[rt.TableId] = map[string]interface{}{"filters": []interface{}{}}
		options := FilterBuildContext{
			SpaceType: spaceType,
			SpaceId:   spaceId,
			TableId:   rt.TableId,
		}
		// 使用统一抽象方法生成filters
		filters := s.buildFiltersByUsage(options, UsageComposeDorisTableIds)
		dataValues[rt.TableId] = map[string]any{"filters": filters}
	}

	// 二段式校验&补充
	dataValuesToRedis := make(map[string]map[string]any)
	for tid, values := range dataValues {
		reformattedTid := reformatTableId(tid)
		dataValuesToRedis[reformattedTid] = values
	}
	logger.Infof("ComposeDorisTableIds: compose doris table_id successfully, data_values->[%v]", dataValues)

	return dataValuesToRedis, nil
}

// ComposeRelatedBkciTableIds 组装关联的BKCI类型的es/doris结果表
func (s *SpacePusher) ComposeRelatedBkciTableIds(spaceType, spaceId string) (map[string]map[string]any, error) {
	logger.Infof("start to push es table_id, space_type [%s], space_id [%s]", spaceType, spaceId)
	var bizIdsList []int
	// 获取关联的BKCI类型的空间ID列表
	relatedSpaces, err := s.GetRelatedSpaces(spaceType, spaceId, models.SpaceTypeBKCI)
	if err != nil {
		logger.Errorf("ComposeRelatedBkciTableIds, get related bkci spaces failed,space_type->[%s],space_id->[%s], err: %s", spaceType, spaceId, err)
		return nil, err
	}

	logger.Infof("ComposeRelatedBkciTableIds,space_type->[%s],space_id->[%s],has related bkci spaces->[%v]", spaceType, spaceId, relatedSpaces)
	for _, bkciSpaceId := range relatedSpaces {
		// 获取该BKCI空间对应的业务ID（负数）
		bizId, err := s.getBizIdBySpace(models.SpaceTypeBKCI, bkciSpaceId)
		if err != nil {
			logger.Errorf("ComposeRelatedBkciTableIds, get biz_id by space [%s] failed, err: %s", bkciSpaceId, err)
			continue
		}
		bizIdsList = append(bizIdsList, bizId)
	}

	// 如果关联的BKCI空间没有业务ID，则不进行处理
	if len(bizIdsList) == 0 {
		logger.Infof("ComposeRelatedBkciTableIds, no related bkci spaces, space_type->[%s],space_id->[%s]", spaceType, spaceId)
		return nil, nil
	}

	db := mysql.GetDBSession().DB
	var rtList []resulttable.ResultTable
	// 查询DB中符合条件的结果表
	if err := resulttable.NewResultTableQuerySet(db).
		Select(resulttable.ResultTableDBSchema.TableId).
		BkBizIdIn(bizIdsList...).
		DefaultStorageIn(models.StorageTypeES, models.StorageTypeDoris).
		IsDeletedEq(false).
		IsEnableEq(true).
		All(&rtList); err != nil {
		return nil, err
	}

	dataValues := make(map[string]map[string]any)
	for _, rt := range rtList {
		// dataValues[rt.TableId] = map[string]interface{}{"filters": []interface{}{}}
		options := FilterBuildContext{
			SpaceType: spaceType,
			SpaceId:   spaceId,
			TableId:   rt.TableId,
		}
		// 使用统一抽象方法生成filters
		filters := s.buildFiltersByUsage(options, UsageComposeEsBkciTableIds)
		dataValues[rt.TableId] = map[string]any{"filters": filters}
	}

	// 二段式校验&补充
	dataValuesToRedis := make(map[string]map[string]any)
	for tid, values := range dataValues {
		reformattedTid := reformatTableId(tid)
		dataValuesToRedis[reformattedTid] = values
	}
	logger.Infof("ComposeRelatedBkciTableIds success, space_type [%s], space_id [%s], data_values->[%v]", spaceType, spaceId, dataValuesToRedis)
	return dataValuesToRedis, nil
}

// GetRelatedSpaces 获取获取{SpaceTypeID}__{spaceID} 关联的{targetSpaceTypeId}类型的空间ID
func (s *SpacePusher) GetRelatedSpaces(spaceTypeID, spaceID, targetSpaceTypeID string) ([]string, error) {
	var filteredResources []space.SpaceResource
	db := mysql.GetDBSession().DB
	if err := space.NewSpaceResourceQuerySet(db).ResourceTypeEq(spaceTypeID).ResourceIdEq(spaceID).SpaceTypeIdEq(targetSpaceTypeID).All(&filteredResources); err != nil {
		logger.Errorf("GetRelatedSpaces, get related spaces for failed, space_type->[%s],space_id ->[%s],err: %s", spaceTypeID, spaceID, err)
		return nil, err
	}

	// 返回space_id列表
	var spaceIDs []string
	for _, resource := range filteredResources {
		spaceIDs = append(spaceIDs, resource.SpaceId)
	}
	return spaceIDs, nil
}

// GetBizIdBySpace 获取空间对应的业务ID列表
func (s *SpacePusher) getBizIdsBySpace(spaceType string, spaceIds []string) ([]int, error) {
	logger.Infof("getBizIdsBySpace: start to get biz_id list by space, space_type [%s], space_ids [%s]", spaceType, spaceIds)
	var bizIds []int
	for _, spaceId := range spaceIds {
		bizId, err := s.GetBizIdBySpace(models.SpaceTypeBKCI, spaceId)
		if err != nil {
			return nil, err
		}
		bizIds = append(bizIds, bizId)
	}
	logger.Infof("getBizIdsBySpace: get biz_id list by space success, space_type [%s], space_ids [%s], biz_ids %v", spaceType, spaceIds, bizIds)
	return bizIds, nil
}

// GetBizIdBySpace 获取空间对应的业务，因为创建后一般不会变动，增加缓存，减少对 db 的影响
func (s *SpacePusher) getBizIdBySpace(spaceType, spaceId string) (int, error) {
	bizId, err := s.GetBizIdBySpace(spaceType, spaceId)
	if err != nil {
		return 0, err
	}

	cache, err := memcache.GetMemCache()
	if err != nil {
		return 0, err
	}

	s.mut.Lock()
	defer s.mut.Unlock()

	ok := false
	var data any
	dataMap := make(map[string]int)
	data, ok = cache.Get(CachedSpaceBizIdKey)
	if ok {
		dataMap, ok = data.(map[string]int)
		if ok {
			bizId, ok := dataMap[fmt.Sprintf("%s__%s", spaceType, spaceId)]
			if ok {
				return bizId, nil
			}
		}
	}

	// 赋值
	dataMap[fmt.Sprintf("%s__%s", spaceType, spaceId)] = bizId
	cache.PutWithTTL(CachedSpaceBizIdKey, dataMap, 0, 24*time.Hour)
	return bizId, err
}

// GetBizIdBySpace getBizIdBySpace get biz id by space
func (s *SpacePusher) GetBizIdBySpace(spaceType, spaceId string) (int, error) {
	db := mysql.GetDBSession().DB
	var spaceObj space.Space
	if err := space.NewSpaceQuerySet(db).SpaceTypeIdEq(spaceType).SpaceIdEq(spaceId).One(&spaceObj); err != nil {
		return 0, err
	}
	if spaceType == models.SpaceTypeBKCC {
		bizId, _ := strconv.ParseInt(spaceObj.SpaceId, 10, 64)
		return int(bizId), nil
	} else {
		return -spaceObj.Id, nil
	}
}

// 获取平台级 data id
func (s *SpacePusher) getPlatformDataIds(bkTenantId, spaceType string) ([]uint, error) {
	// 获取平台级的数据源
	// 仅针对当前空间类型，比如 bkcc，特殊的是 all 类型
	db := mysql.GetDBSession().DB
	var bkDataIdList []uint
	var dsList []resulttable.DataSource
	qs := resulttable.NewDataSourceQuerySet(db).Select(resulttable.DataSourceDBSchema.BkDataId, resulttable.DataSourceDBSchema.SpaceTypeId).BkTenantIdEq(bkTenantId).IsPlatformDataIdEq(true)

	// 多住模式下去除单租户使用的全局数据源
	if cfg.EnableMultiTenantMode {
		qs = qs.BkDataIdNotIn(1001, 1002, 1003, 1004, 1005, 1006, 1007, 1013, 1008, 1009, 1010, 1011, 1100003, 1100005, 1100000)
	}

	// 针对 bkcc 类型，这要是插件，不属于某个业务空间，也没有传递空间类型，因此，需要包含 all 类型
	if spaceType != "" && spaceType != models.SpaceTypeBKCC {
		qs = qs.SpaceTypeIdEq(spaceType)
	}
	if err := qs.All(&dsList); err != nil {
		return nil, err
	}
	for _, ds := range dsList {
		bkDataIdList = append(bkDataIdList, ds.BkDataId)
	}
	return bkDataIdList, nil
}

type DataIdDetail struct {
	EtlConfig        string `json:"etl_config"`
	SpaceUid         string `json:"space_uid"`
	IsPlatformDataId bool   `json:"is_platform_data_id"`
}

// reformat_table_id 用于校验并补充二段式的逻辑
func reformatTableId(tid string) string {
	parts := strings.Split(tid, ".")
	if len(parts) == 1 {
		// 如果长度为 1，补充 `.__default__`
		logger.Infof("reformatTableId: table_id [%s] is missing '.', adding '.__default__'", tid)
		return fmt.Sprintf("%s.__default__", tid)
	} else if len(parts) != 2 {
		// 如果长度不是 2，记录错误日志并返回原始值
		logger.Errorf("reformatTableId: table_id [%s] is invalid, contains too many dots", tid)
		return tid // 保持原样
	}
	// 如果已经是二段式，直接返回
	return tid
}

// UsageComposeData 组装业务关联数据路由
func (s *SpacePusher) composeData(bkTenantId string, spaceType, spaceId string, tableIdList []string, defaultFilters []map[string]any, options *optionx.Options) (map[string]map[string]any, error) {
	logger.Infof("composeData space_type [%s], space_id [%s], table_id_list [%s]", spaceType, spaceId, tableIdList)
	if options == nil {
		options = optionx.NewOptions(nil)
	}
	options.SetDefault("includePlatformDataId", true)

	includePlatformDataId, _ := options.GetBool("includePlatformDataId")
	// 过滤到对应的结果表
	ops := optionx.NewOptions(map[string]any{"includePlatformDataId": includePlatformDataId})
	if need, ok := options.GetBool("fromAuthorization"); ok {
		ops.Set("fromAuthorization", need)
	}
	tableIdDataId, err := s.GetSpaceTableIdDataId(bkTenantId, spaceType, spaceId, tableIdList, nil, ops)
	if err != nil {
		logger.Errorf("composeData: GetSpaceTableIdDataId failed,space_type [%s], space_id [%s],err[%v]", spaceType, spaceId, err)
		return nil, err
	}
	valueData := make(map[string]map[string]any)
	// 如果为空，返回默认值
	if len(tableIdDataId) == 0 {
		logger.Errorf("space_type [%s], space_id [%s] not found table_id and data_id", spaceType, spaceId)
		return valueData, nil
	}
	var tableIds []string
	for tableId := range tableIdDataId {
		tableIds = append(tableIds, tableId)
	}
	// 提取具备VM、ES、InfluxDB的链路结果表
	tableIds, err = s.refineTableIds(tableIds)
	// 再一次过滤，过滤到有链路的结果表，并且写入 influxdb&vm&es 的数据
	tableIdDataIdMap := make(map[string]uint)
	var dataIdList []uint
	for _, tableId := range tableIds {
		dataId := tableIdDataId[tableId]
		tableIdDataIdMap[tableId] = dataId
		dataIdList = append(dataIdList, dataId)
	}
	if len(dataIdList) == 0 {
		return valueData, nil
	}
	db := mysql.GetDBSession().DB
	var dsList []resulttable.DataSource
	if err := resulttable.NewDataSourceQuerySet(db).Select(resulttable.DataSourceDBSchema.BkDataId, resulttable.DataSourceDBSchema.EtlConfig, resulttable.DataSourceDBSchema.SpaceUid, resulttable.DataSourceDBSchema.IsPlatformDataId).BkTenantIdEq(bkTenantId).BkDataIdIn(dataIdList...).All(&dsList); err != nil {
		return nil, err
	}
	// 获取datasource的信息，避免后续每次都去查询db
	dataIdDetail := make(map[uint]*DataIdDetail)
	for _, ds := range dsList {
		dataIdDetail[ds.BkDataId] = &DataIdDetail{
			EtlConfig:        ds.EtlConfig,
			SpaceUid:         ds.SpaceUid,
			IsPlatformDataId: ds.IsPlatformDataId,
		}
	}
	// 查询结果表，同时获取 bk_biz_id_alias 字段
	var rtList []resulttable.ResultTable
	if err := resulttable.NewResultTableQuerySet(db).
		Select(
			resulttable.ResultTableDBSchema.TableId,
			resulttable.ResultTableDBSchema.SchemaType,
			resulttable.ResultTableDBSchema.DataLabel,
			resulttable.ResultTableDBSchema.BkBizIdAlias, // 查询 bk_biz_id_alias
		).
		BkTenantIdEq(bkTenantId).
		TableIdIn(tableIds...).
		All(&rtList); err != nil {
		return nil, err
	}
	// 构建 table_id -> bk_biz_id_alias 的映射
	bkBizIdAliasMap := make(map[string]string)
	for _, rt := range rtList {
		bkBizIdAliasMap[rt.TableId] = rt.BkBizIdAlias
	}
	// 获取结果表对应的类型
	measurementTypeMap, err := s.getMeasurementTypeByTableId(bkTenantId, tableIds, rtList, tableIdDataIdMap)
	if err != nil {
		return nil, err
	}
	// 获取空间所属的数据源 ID
	var spdsList []space.SpaceDataSource
	if err := space.NewSpaceDataSourceQuerySet(db).Select(space.SpaceDataSourceDBSchema.BkDataId).SpaceTypeIdEq(spaceType).SpaceIdEq(spaceId).FromAuthorizationEq(false).All(&spdsList); err != nil {
		return nil, err
	}
	for _, tid := range tableIds {
		// NOTE: 特殊逻辑，忽略跨空间类型的 bkci 的结果表
		if strings.HasPrefix(tid, models.Bkci1001TableIdPrefix) {
			continue
		}
		// NOTE: 特殊逻辑，针对 `dbm_system` 开头的结果表，授权给DBM业务访问全部数据,之所以没走常规授权,是因为这部分数据的DataId都是1001
		spaceUid := fmt.Sprintf("%s__%s", spaceType, spaceId)
		if strings.HasPrefix(tid, models.Dbm1001TableIdPrefix) && stringx.StringInSlice(spaceUid, cfg.GlobalAccessDbmRtSpaceUid) {
			valueData[tid] = map[string]any{"filters": []any{}}
			continue
		}
		// 如果查询不到类型，则忽略
		measurementType, ok := measurementTypeMap[tid]
		if !ok {
			logger.Errorf("table_id [%s] not find measurement type", tid)
			continue
		}
		// 如果没有对应的结果表，则忽略
		dataId, ok := tableIdDataIdMap[tid]
		if !ok {
			logger.Errorf("table_id [%s] not found data_id", tid)
			continue
		}
		detail := dataIdDetail[dataId]
		var isExistSpace bool
		for _, spds := range spdsList {
			if spds.BkDataId == dataId {
				isExistSpace = true
				break
			}
		}
		// 拼装过滤条件, 如果有指定，则按照指定数据设置过滤条件
		if len(defaultFilters) != 0 { // 当存在指定过滤条件时,则使用指定过滤条件
			valueData[tid] = map[string]any{"filters": defaultFilters}
		} else {
			filters := make([]map[string]any, 0)
			if s.isNeedFilterForBkcc(measurementType, spaceType, spaceId, detail, isExistSpace) {
				// 若需要拼接过滤条件,那么调用通用filters生成方法,生成filters
				builderContext := FilterBuildContext{
					SpaceType: spaceType,
					SpaceId:   spaceId,
					TableId:   tid,
					FilterAlias: func() string {
						if alias, ok := bkBizIdAliasMap[tid]; ok && alias != "" {
							return alias
						}
						return "bk_biz_id"
					}(),
				}

				newFilters := s.buildFiltersByUsage(builderContext, UsageComposeData)
				filters = append(filters, newFilters...)
			}
			valueData[tid] = map[string]any{"filters": filters}
		}
	}

	// 二段式补充&校验
	valueDataToRedis := make(map[string]map[string]any)
	for tid, value := range valueData {
		// 处理key
		reformattedTid := reformatTableId(tid)
		valueDataToRedis[reformattedTid] = value
	}

	logger.Infof("space_type [%s], space_id [%s], table_id_list [%s], value_data [%+v]", spaceType, spaceId, tableIdList, valueDataToRedis)
	return valueDataToRedis, nil
}

// 针对业务类型空间判断是否需要添加过滤条件
func (s *SpacePusher) isNeedFilterForBkcc(measurementType, spaceType, spaceId string, dataIdDetail *DataIdDetail, isExistSpace bool) bool {
	if dataIdDetail == nil {
		return true
	}

	// 为防止查询范围放大，先功能开关控制，针对归属到具体空间的数据源，不需要添加过滤条件
	if !cfg.GlobalIsRestrictDsBelongSpace && (dataIdDetail.SpaceUid == fmt.Sprintf("%s__%s", spaceType, spaceId)) {
		return false
	}

	// 如果不是自定义时序或exporter，则不需要关注类似的情况，必须增加过滤条件
	tsMeasurementTypes := []string{models.MeasurementTypeBkSplit, models.MeasurementTypeBkStandardV2TimeSeries, models.MeasurementTypeBkExporter}
	if dataIdDetail.EtlConfig != models.ETLConfigTypeBkStandardV2TimeSeries {
		var exist bool
		for _, tp := range tsMeasurementTypes {
			if tp == measurementType {
				exist = true
				break
			}
		}
		if !exist {
			return true
		}
	}
	// 对自定义插件的处理，兼容黑白名单对类型的更改
	// 黑名单时，会更改为单指标单表
	if (dataIdDetail.IsPlatformDataId && measurementType == models.MeasurementTypeBkExporter) || (dataIdDetail.EtlConfig == models.ETLConfigTypeBkExporter && measurementType == models.MeasurementTypeBkSplit) {
		// 如果space_id与data_id所属空间UID相同，则不需要过滤
		if dataIdDetail.SpaceUid == fmt.Sprintf("%s__%s", spaceType, spaceId) {
			return false
		}
		return true
	}
	// 可以执行到以下代码，必然是自定义时序的数据源
	// 1. 非公共的(全空间或指定空间类型)自定义时序，查询时，不需要任何查询条件
	if !dataIdDetail.IsPlatformDataId {
		return false
	}

	// 2. 公共自定义时序，如果属于当前space，不需要添加过滤条件
	if isExistSpace {
		return false
	}
	// 3. 此时，必然是自定义时序，且是公共的平台数据源，同时非该当前空间下，需要添加过滤条件
	return true
}

// composeBcsSpaceBizTableIds 推送 bcs 类型空间下的集群数据
func (s *SpacePusher) composeBcsSpaceBizTableIds(spaceType, spaceId string) (map[string]map[string]any, error) {
	logger.Infof("start to push cluster of bcs space table_id, space_type [%s], space_id [%s]", spaceType, spaceId)
	// 首先获取关联业务的数据
	resourceType := models.SpaceTypeBKCC
	db := mysql.GetDBSession().DB
	var sr space.SpaceResource
	// 设置默认值，如果有异常，则返回默认值
	dataValues := make(map[string]map[string]any)
	if err := space.NewSpaceResourceQuerySet(db).SpaceTypeIdEq(spaceType).SpaceIdEq(spaceId).ResourceTypeEq(resourceType).One(&sr); err != nil {
		if gorm.IsRecordNotFoundError(err) {
			logger.Errorf("space: [%s__%s], resource_type [%s] not found", spaceType, spaceId, resourceType)
			return dataValues, nil
		}
		return dataValues, err
	}
	// 获取空间关联的业务，注意这里业务 ID 为字符串类型
	var bizIdStr string
	if sr.ResourceId != nil {
		bizIdStr = *sr.ResourceId
	}
	// 现阶段支持主机和部分插件授权给蓝盾使用
	var rtList []resulttable.ResultTable
	likeTableIds := []string{fmt.Sprintf("%s%%", models.SystemTableIdPrefix)}

	// 特殊授权逻辑,读取环境配置,将部分业务RT授权给CI空间访问
	for _, tableId := range cfg.BkciSpaceAccessPlugins {
		logger.Infof("composeBcsSpaceBizTableIds: try to add table_id->[%s] to router for space_type->[%s],space_id->[%s]", tableId, spaceType, spaceId)
		likeTableIds = append(likeTableIds, fmt.Sprintf("%s%%", tableId))
	}

	if err := resulttable.NewResultTableQuerySet(db).Select(resulttable.ResultTableDBSchema.TableId).TableIdsLike(likeTableIds).All(&rtList); err != nil {
		return nil, err
	}
	for _, rt := range rtList {
		// dataValues[rt.TableId] = map[string]interface{}{"filters": []map[string]interface{}{{"bk_biz_id": bizIdStr}}}
		options := FilterBuildContext{
			SpaceType:   spaceType,
			SpaceId:     spaceId,
			TableId:     rt.TableId,
			BkBizId:     bizIdStr,    // 归属的业务ID
			FilterAlias: "bk_biz_id", // 过滤条件的别名
		}
		filters := s.buildFiltersByUsage(options, UsageComposeBcsSpaceBizTableIds)
		dataValues[rt.TableId] = map[string]any{"filters": filters}
	}

	// 二段式校验&补充
	dataValuesToRedis := make(map[string]map[string]any)
	for tid, values := range dataValues {
		reformattedTid := reformatTableId(tid)
		dataValuesToRedis[reformattedTid] = values
	}
	return dataValuesToRedis, nil
}

// composeBksaasSpaceClusterTableIds 推送 bksaas空间关联的集群数据
func (s *SpacePusher) composeBksaasSpaceClusterTableIds(spaceType, spaceId string, tableIdList []string) (map[string]map[string]any, error) {
	logger.Infof("start to push cluster of bksaas space table_id, space_type [%s], space_id [%s]", spaceType, spaceId)
	// 获取空间的集群数据
	resourceType := models.SpaceTypeBKSAAS
	// 优先进行判断项目相关联的容器资源，减少等待
	db := mysql.GetDBSession().DB
	var sr space.SpaceResource
	dataValues := make(map[string]map[string]any)
	if err := space.NewSpaceResourceQuerySet(db).SpaceTypeIdEq(spaceType).SpaceIdEq(spaceId).ResourceTypeEq(resourceType).ResourceIdEq(spaceId).One(&sr); err != nil {
		if gorm.IsRecordNotFoundError(err) {
			logger.Errorf("space: [%s__%s], resource_type [%s] not found", spaceType, spaceId, resourceType)
			return dataValues, nil
		}
		return dataValues, err
	}
	var resList []map[string]any
	if err := jsonx.UnmarshalString(sr.DimensionValues, &resList); err != nil {
		return nil, errors.Wrap(err, "unmarshal space resource dimension failed")
	}
	// 如果关键维度数据为空，同样返回默认
	if len(resList) == 0 {
		return dataValues, nil
	}
	// 获取集群的数据, 格式: {cluster_id: {"bcs_cluster_id": xxx, "namespace": xxx}}
	clusterInfoMap := make(map[string]any)
	var clusterIdList []string
	for _, res := range resList {
		resOptions := optionx.NewOptions(res)
		clusterId, ok := resOptions.GetString("cluster_id")
		if !ok {
			logger.Errorf("parse space resource cluster values failed, %v", res)
			continue
		}
		clusterType, ok := resOptions.GetString("cluster_type")
		if !ok {
			clusterType = models.BcsClusterTypeSingle
		}
		namespaceList, ok := resOptions.GetInterfaceSliceWithString("namespace")
		if !ok {
			logger.Errorf("parse space resource dimension values failed, %v", res)
			continue
		}

		//if clusterType == models.BcsClusterTypeShared && len(namespaceList) != 0 {
		//	var nsDataList []map[string]interface{}
		//	for _, ns := range namespaceList {
		//		nsDataList = append(nsDataList, map[string]interface{}{"bcs_cluster_id": clusterId, "namespace": ns})
		//	}
		//	clusterInfoMap[clusterId] = nsDataList
		//} else if clusterType == models.BcsClusterTypeSingle {
		//	clusterInfoMap[clusterId] = []map[string]interface{}{{"bcs_cluster_id": clusterId, "namespace": nil}}
		//}

		// 当集群类型为共享集群,且允许访问的namespace列表为空时,跳过
		if clusterType == models.BcsClusterTypeShared && len(namespaceList) == 0 {
			continue
		}

		options := FilterBuildContext{
			SpaceType:     spaceType,
			SpaceId:       spaceId,
			ClusterId:     clusterId,
			NamespaceList: namespaceList,
			IsShared:      clusterType == models.BcsClusterTypeShared,
		}
		// 使用统一抽象方法生成filters
		filters := s.buildFiltersByUsage(options, UsageComposeBksaasSpaceClusterTableIds)
		clusterInfoMap[clusterId] = filters

		clusterIdList = append(clusterIdList, clusterId)
	}
	dataIdClusterIdMap, err := s.getClusterDataIds(clusterIdList, tableIdList)
	if err != nil {
		return nil, err
	}
	if len(dataIdClusterIdMap) == 0 {
		logger.Errorf("space [%s__%s] not found cluster", spaceType, spaceId)
		return dataValues, nil
	}
	var dataIdList []uint
	for dataId := range dataIdClusterIdMap {
		dataIdList = append(dataIdList, dataId)
	}
	// 获取结果表及数据源
	tableIdDataIdMap, err := s.getResultTablesByDataIds(dataIdList, nil)
	if err != nil {
		return nil, err
	}
	for tid, dataId := range tableIdDataIdMap {
		clusterId, ok := dataIdClusterIdMap[dataId]
		if !ok {
			continue
		}
		// 获取对应的集群及命名空间信息
		filters := clusterInfoMap[clusterId]
		if filters == nil {
			filters = make([]any, 0)
		}
		dataValues[tid] = map[string]any{"filters": filters}
	}
	// 二段式校验&补充
	dataValuesToRedis := make(map[string]map[string]any)
	for tid, values := range dataValues {
		reformattedTid := reformatTableId(tid)
		dataValuesToRedis[reformattedTid] = values
	}
	return dataValuesToRedis, nil
}

// composeBcsSpaceClusterTableIds 推送BKCI空间关联的集群信息,包括共享集群等
func (s *SpacePusher) composeBcsSpaceClusterTableIds(spaceType, spaceId string) (map[string]map[string]any, error) {
	logger.Infof("start to push cluster of bcs space table_id, space_type [%s], space_id [%s]", spaceType, spaceId)
	// 获取空间的集群数据
	resourceType := models.SpaceTypeBCS
	// 优先进行判断项目相关联的容器资源，减少等待
	db := mysql.GetDBSession().DB
	dataValues := make(map[string]map[string]any)
	var sr space.SpaceResource
	if err := space.NewSpaceResourceQuerySet(db).SpaceTypeIdEq(spaceType).SpaceIdEq(spaceId).ResourceTypeEq(resourceType).ResourceIdEq(spaceId).One(&sr); err != nil {
		if gorm.IsRecordNotFoundError(err) {
			logger.Errorf("space: [%s__%s], resource_type [%s] not found", spaceType, spaceId, resourceType)
			return dataValues, nil
		}
		return nil, err
	}
	var resList []map[string]any
	if err := jsonx.UnmarshalString(sr.DimensionValues, &resList); err != nil {
		return nil, errors.Wrap(err, "unmarshal space resource dimension failed")
	}
	// 如果关键维度数据为空，同样返回默认
	if len(resList) == 0 {
		return dataValues, nil
	}
	// 获取集群的数据, 格式: {cluster_id: {"bcs_cluster_id": xxx, "namespace": xxx}}
	clusterInfoMap := make(map[string]any)
	var clusterIdList []string
	for _, res := range resList {
		resOptions := optionx.NewOptions(res)
		clusterId, ok := resOptions.GetString("cluster_id")
		if !ok {
			return nil, errors.Errorf("parse space resource dimension values failed, %v", res)
		}
		clusterType, ok := resOptions.GetString("cluster_type")
		if !ok {
			clusterType = models.BcsClusterTypeSingle
		}
		namespaceList, nsOk := resOptions.GetStringSlice("namespace")
		if !nsOk {
			namespaceList = []string{}
		}

		//if clusterType == models.BcsClusterTypeShared && len(namespaceList) != 0 {
		//	var nsDataList []map[string]interface{}
		//	for _, ns := range namespaceList {
		//		nsDataList = append(nsDataList, map[string]interface{}{"bcs_cluster_id": clusterId, "namespace": ns})
		//	}
		//	clusterInfoMap[clusterId] = nsDataList
		//	clusterIdList = append(clusterIdList, clusterId)
		//} else if clusterType == models.BcsClusterTypeSingle {
		//	clusterInfoMap[clusterId] = []map[string]interface{}{{"bcs_cluster_id": clusterId, "namespace": nil}}
		//	clusterIdList = append(clusterIdList, clusterId)
		//}

		// 若是共享集群,但是namespaces列表为空,跳过
		if clusterType == models.BcsClusterTypeShared && len(namespaceList) == 0 {
			continue
		}

		options := FilterBuildContext{
			SpaceType:     spaceType,
			SpaceId:       spaceId,
			ClusterId:     clusterId,
			NamespaceList: namespaceList,
			IsShared:      clusterType == models.BcsClusterTypeShared,
		}
		// 使用统一抽象方法生成filters
		filters := s.buildFiltersByUsage(options, UsageComposeBcsSpaceClusterTableIds)
		clusterInfoMap[clusterId] = filters
		clusterIdList = append(clusterIdList, clusterId)

	}
	dataIdClusterIdMap, err := s.getClusterDataIds(clusterIdList, nil)
	if err != nil {
		return nil, err
	}

	if len(dataIdClusterIdMap) == 0 {
		logger.Errorf("space [%s__%s] not found cluster", spaceType, spaceId)
		return dataValues, nil
	}
	var dataIdList []uint
	for dataId := range dataIdClusterIdMap {
		dataIdList = append(dataIdList, dataId)
	}
	// 获取结果表及数据源
	tableIdDataIdMap, err := s.getResultTablesByDataIds(dataIdList, nil)
	if err != nil {
		return nil, err
	}
	for tid, dataId := range tableIdDataIdMap {
		clusterId, ok := dataIdClusterIdMap[dataId]
		if !ok {
			continue
		}
		// 获取对应的集群及命名空间信息
		filters, ok := clusterInfoMap[clusterId]
		if !ok {
			filters = make([]any, 0)
		}
		dataValues[tid] = map[string]any{"filters": filters}
	}
	// 二段式校验&补充
	dataValuesToRedis := make(map[string]map[string]any)
	for tid, values := range dataValues {
		reformattedTid := reformatTableId(tid)
		dataValuesToRedis[reformattedTid] = values
	}
	return dataValuesToRedis, nil
}

// 获取集群及数据源
func (s *SpacePusher) getClusterDataIds(clusterIdList, tableIdList []string) (map[uint]string, error) {
	// 如果指定结果表, 则仅过滤结果表对应的数据源
	db := mysql.GetDBSession().DB
	var dataIdList []uint
	if len(tableIdList) != 0 {
		var dsrtList []resulttable.DataSourceResultTable
		for _, chunkTableIdList := range slicex.ChunkSlice(tableIdList, 0) {
			var tempList []resulttable.DataSourceResultTable
			if err := resulttable.NewDataSourceResultTableQuerySet(db).Select(resulttable.DataSourceResultTableDBSchema.BkDataId).TableIdIn(chunkTableIdList...).All(&tempList); err != nil {
				return nil, err
			}
			dsrtList = append(dsrtList, tempList...)
		}
		for _, dsrt := range dsrtList {
			dataIdList = append(dataIdList, dsrt.BkDataId)
		}
	} else if len(clusterIdList) != 0 {
		// 如果集群存在，则获取集群下的内置和自定义数据源
		var clusterList []bcs.BCSClusterInfo
		if err := bcs.NewBCSClusterInfoQuerySet(db).Select(bcs.BCSClusterInfoDBSchema.K8sMetricDataID, bcs.BCSClusterInfoDBSchema.CustomMetricDataID).StatusNotIn(models.BcsClusterStatusDeleted, models.BcsRawClusterStatusDeleted).ClusterIDIn(clusterIdList...).All(&clusterList); err != nil {
			return nil, err
		}
		for _, cluster := range clusterList {
			dataIdList = append(dataIdList, cluster.K8sMetricDataID)
			dataIdList = append(dataIdList, cluster.CustomMetricDataID)
		}
	}
	if len(dataIdList) == 0 {
		return make(map[uint]string), nil
	}
	// 过滤到集群的数据源，仅包含两类，集群内置和集群自定义
	dataIdClusterIdMap := make(map[uint]string)

	var clusterListA []bcs.BCSClusterInfo
	// 已经限制了data id, 也就是状态已经确认，不需要在根据状态进行过滤
	if err := bcs.NewBCSClusterInfoQuerySet(db).Select(bcs.BCSClusterInfoDBSchema.K8sMetricDataID, bcs.BCSClusterInfoDBSchema.ClusterID).K8sMetricDataIDIn(dataIdList...).All(&clusterListA); err != nil {
		return nil, err
	}
	for _, cluster := range clusterListA {
		dataIdClusterIdMap[cluster.K8sMetricDataID] = cluster.ClusterID
	}

	var clusterListB []bcs.BCSClusterInfo
	if err := bcs.NewBCSClusterInfoQuerySet(db).Select(bcs.BCSClusterInfoDBSchema.CustomMetricDataID, bcs.BCSClusterInfoDBSchema.ClusterID).CustomMetricDataIDIn(dataIdList...).All(&clusterListB); err != nil {
		return nil, err
	}
	for _, cluster := range clusterListB {
		dataIdClusterIdMap[cluster.CustomMetricDataID] = cluster.ClusterID
	}

	return dataIdClusterIdMap, nil
}

// 通过数据源 ID 获取结果表数据
func (s *SpacePusher) getResultTablesByDataIds(dataIdList []uint, tableIdList []string) (map[string]uint, error) {
	db := mysql.GetDBSession().DB
	var dsrtList []resulttable.DataSourceResultTable
	qs := resulttable.NewDataSourceResultTableQuerySet(db).Select(resulttable.DataSourceResultTableDBSchema.BkDataId, resulttable.DataSourceResultTableDBSchema.TableId)
	if len(dataIdList) != 0 {
		qs = qs.BkDataIdIn(dataIdList...)
	}
	if len(tableIdList) != 0 {
		qs = qs.TableIdIn(tableIdList...)
	}
	if err := qs.All(&dsrtList); err != nil {
		return nil, err
	}
	dataMap := make(map[string]uint)
	for _, dsrt := range dsrtList {
		dataMap[dsrt.TableId] = dsrt.BkDataId
	}
	return dataMap, nil
}

// composeBkciLevelTableIds 组装 bkci 全局下的结果表
func (s *SpacePusher) composeBkciLevelTableIds(bkTenantId, spaceType, spaceId string) (map[string]map[string]any, error) {
	logger.Infof("start to push bkci level table_id, space_type [%s], space_id [%s]", spaceType, spaceId)
	// 过滤空间级的数据源
	dataIds, err := s.getPlatformDataIds(bkTenantId, spaceType)
	if err != nil {
		return nil, err
	}
	dataValues := make(map[string]map[string]any)
	if len(dataIds) == 0 {
		return dataValues, nil
	}
	db := mysql.GetDBSession().DB
	var dsrtList []resulttable.DataSourceResultTable
	if err := resulttable.NewDataSourceResultTableQuerySet(db).Select(resulttable.DataSourceResultTableDBSchema.TableId).BkTenantIdEq(bkTenantId).BkDataIdIn(dataIds...).All(&dsrtList); err != nil {
		return nil, err
	}
	if len(dsrtList) == 0 {
		return dataValues, nil
	}
	var tableIds []string
	for _, dsrt := range dsrtList {
		tableIds = append(tableIds, dsrt.TableId)
	}
	// 过滤仅写入influxdb和vm的数据
	tableIds, err = s.refineTableIds(tableIds)
	if err != nil {
		return nil, err
	}
	for _, tid := range tableIds {
		// dataValues[tid] = map[string]interface{}{"filters": []map[string]interface{}{{"projectId": spaceId}}}
		filterAlias := "projectId"
		if slices.Contains(cfg.SpecialRtRouterAliasResultTableList, tid) {
			logger.Infof("composeBkciLevelTableIds: table_id->[%s] in special rt router alias list, use rt bk_biz_id_alias as filter alias", tid)
			var rt resulttable.ResultTable
			if err := resulttable.NewResultTableQuerySet(db).Select(resulttable.ResultTableDBSchema.BkBizIdAlias).BkTenantIdEq(bkTenantId).TableIdEq(tid).One(&rt); err != nil {
				logger.Errorf("composeBkciLevelTableIds get bk_biz_id_alias for table_id [%s] error, %s", tid, err)
				continue
			}
			filterAlias = rt.BkBizIdAlias
		}
		options := FilterBuildContext{
			SpaceType:   spaceType,
			SpaceId:     spaceId,
			TableId:     tid,
			FilterAlias: filterAlias,
		}
		// 使用统一抽象方法生成filters
		filters := s.buildFiltersByUsage(options, UsageComposeBkciLevelTableIds)
		dataValues[tid] = map[string]any{"filters": filters}
	}

	// 二段式校验&补充
	dataValuesToRedis := make(map[string]map[string]any)
	for tid, values := range dataValues {
		reformattedTid := reformatTableId(tid)
		dataValuesToRedis[reformattedTid] = values
	}
	return dataValuesToRedis, nil
}

// composeBkciOtherTableIds 组装BKCI级别结果表
func (s *SpacePusher) composeBkciOtherTableIds(bkTenantId, spaceType, spaceId string) (map[string]map[string]any, error) {
	logger.Infof("start to push bkci other table_id, space_type [%s], space_id [%s]", spaceType, spaceId)
	// 针对集群缓存对应的数据源，避免频繁的访问db
	excludeDataIdList, err := s.getCachedClusterDataIdList()
	if err != nil {
		logger.Errorf("composeBkciOtherTableIds get cached cluster data id list error, %s", err)
		return nil, err
	}
	options := optionx.NewOptions(map[string]any{"includePlatformDataId": false, "fromAuthorization": false})
	tableIdDataIdMap, err := s.GetSpaceTableIdDataId(bkTenantId, spaceType, spaceId, nil, excludeDataIdList, options)
	if err != nil {
		return nil, err
	}
	dataValues := make(map[string]map[string]any)
	if len(tableIdDataIdMap) == 0 {
		logger.Errorf("space_type [%s], space_id [%s] not found table_id and data_id", spaceType, spaceId)
		return dataValues, nil
	}

	tableIds := mapx.GetMapKeys(tableIdDataIdMap)
	tableIds, err = s.refineTableIds(tableIds)
	if err != nil {
		return nil, err
	}
	for _, tid := range tableIds {
		// NOTE: 现阶段针对1001下 `system.` 或者 `dbm_system.` 开头的结果表不允许被覆盖
		if strings.HasPrefix(tid, models.SystemTableIdPrefix) || strings.HasPrefix(tid, models.Dbm1001TableIdPrefix) {
			continue
		}
		// dataValues[tid] = map[string]interface{}{"filters": []map[string]interface{}{}}
		options := FilterBuildContext{
			SpaceType: spaceType,
			SpaceId:   spaceId,
			TableId:   tid,
		}
		filters := s.buildFiltersByUsage(options, UsageComposeBkciOtherTableIds)
		dataValues[tid] = map[string]any{"filters": filters}
	}

	// 二段式校验&补充
	dataValuesToRedis := make(map[string]map[string]any)
	for tid, values := range dataValues {
		reformattedTid := reformatTableId(tid)
		dataValuesToRedis[reformattedTid] = values
	}

	return dataValuesToRedis, nil
}

func (s *SpacePusher) composeBkciCrossTableIds(bkTenantId, spaceType, spaceId string) (map[string]map[string]any, error) {
	logger.Infof("start to push bkci cross table_id, space_type [%s], space_id [%s]", spaceType, spaceId)
	db := mysql.GetDBSession().DB
	var rtList []resulttable.ResultTable
	if err := resulttable.NewResultTableQuerySet(db).Select(resulttable.ResultTableDBSchema.TableId).BkTenantIdEq(bkTenantId).TableIdLike(fmt.Sprintf("%s%%", models.Bkci1001TableIdPrefix)).All(&rtList); err != nil {
		return nil, err
	}
	dataValues := make(map[string]map[string]any)
	for _, rt := range rtList {
		// dataValues[rt.TableId] = map[string]interface{}{"filters": []map[string]interface{}{{"projectId": spaceId}}}
		options := FilterBuildContext{
			SpaceType:   spaceType,
			SpaceId:     spaceId,
			TableId:     rt.TableId,
			FilterAlias: "projectId",
		}
		// 使用统一抽象方法生成filters
		filters := s.buildFiltersByUsage(options, UsageComposeBkciCrossTableIds)
		dataValues[rt.TableId] = map[string]any{"filters": filters}
	}

	// 添加P4主机数据相关
	var rtP4List []resulttable.ResultTable
	if err := resulttable.NewResultTableQuerySet(db).Select(resulttable.ResultTableDBSchema.TableId).BkTenantIdEq(bkTenantId).TableIdLike(fmt.Sprintf("%s%%", models.P4SystemTableIdPrefixToBkCi)).All(&rtP4List); err != nil {
		// 当有异常时，返回已有的数据
		return dataValues, err
	}
	for _, rt := range rtP4List {
		// dataValues[rt.TableId] = map[string]interface{}{"filters": []map[string]interface{}{{"devops_id": spaceId}}}
		options := FilterBuildContext{
			SpaceType:   spaceType,
			SpaceId:     spaceId,
			TableId:     rt.TableId,
			FilterAlias: "devops_id",
		}
		// 使用统一抽象方法生成filters
		filters := s.buildFiltersByUsage(options, UsageComposeBkciCrossTableIds)
		dataValues[rt.TableId] = map[string]any{"filters": filters}
	}

	// 二段式校验&补充
	dataValuesToRedis := make(map[string]map[string]any)
	for tid, values := range dataValues {
		reformattedTid := reformatTableId(tid)
		dataValuesToRedis[reformattedTid] = values
	}

	return dataValuesToRedis, nil
}

// 获取缓存的集群对应的数据源 ID
func (s *SpacePusher) getCachedClusterDataIdList() ([]uint, error) {
	cache, cacheErr := memcache.GetMemCache()
	// 存放
	ok := false
	var data any
	if cacheErr == nil {
		data, ok = cache.Get(CachedClusterDataIdKey)
		if ok {
			return data.([]uint), nil
		}
	}
	// 从 db 中获取数据
	var clusterDataIdList []uint
	var clusters []bcs.BCSClusterInfo
	if err := bcs.NewBCSClusterInfoQuerySet(mysql.GetDBSession().DB).All(&clusters); err != nil {
		return nil, err
	}
	for _, c := range clusters {
		clusterDataIdList = append(clusterDataIdList, c.K8sMetricDataID)
		clusterDataIdList = append(clusterDataIdList, c.CustomMetricDataID)
	}

	// 把数据添加到缓存中, 设置超时时间为60min
	if cacheErr == nil {
		cache.PutWithTTL(CachedClusterDataIdKey, clusterDataIdList, 0, 60*time.Minute)
	}
	return clusterDataIdList, nil
}

// composeBksaasOtherTableIds 组装蓝鲸应用非集群数据
func (s *SpacePusher) composeBksaasOtherTableIds(bkTenantId, spaceType, spaceId string, tableIdList []string) (map[string]map[string]any, error) {
	logger.Infof("start to push bksaas other table_id, space_type [%s], space_id [%s]", spaceType, spaceId)
	// 针对集群缓存对应的数据源，避免频繁的访问db
	excludeDataIdList, err := s.getCachedClusterDataIdList()
	if err != nil {
		logger.Errorf("composeBksaasOtherTableIds get cached cluster data id list error, %s", err)
		return nil, err
	}
	options := optionx.NewOptions(map[string]any{"includePlatformDataId": false})
	tableIdDataIdMap, err := s.GetSpaceTableIdDataId(bkTenantId, spaceType, spaceId, tableIdList, excludeDataIdList, options)
	if err != nil {
		return nil, err
	}
	dataValues := make(map[string]map[string]any)
	if len(tableIdDataIdMap) == 0 {
		logger.Errorf("space_type [%s], space_id [%s] not found table_id and data_id", spaceType, spaceId)
		return dataValues, nil
	}
	tableIds := mapx.GetMapKeys(tableIdDataIdMap)
	// 提取仅包含写入 influxdb 和 vm 的结果表
	tableIds, err = s.refineTableIds(tableIds)
	if err != nil {
		return nil, err
	}
	for _, tid := range tableIds {
		// 针对非集群的数据，不限制过滤条件
		// dataValues[tid] = map[string]interface{}{"filters": []map[string]interface{}{}}
		options := FilterBuildContext{
			SpaceType: spaceType,
			SpaceId:   spaceId,
			TableId:   tid,
		}
		filters := s.buildFiltersByUsage(options, UsageComposeBksaasOtherTableIds)
		dataValues[tid] = map[string]any{"filters": filters}
	}

	// 二段式校验&补充
	dataValuesToRedis := make(map[string]map[string]any)
	for tid, values := range dataValues {
		reformattedTid := reformatTableId(tid)
		dataValuesToRedis[reformattedTid] = values
	}
	return dataValuesToRedis, nil
}

// composeAllTypeTableIds 组装指定全空间的可以访问的结果表数据
func (s *SpacePusher) composeAllTypeTableIds(spaceType, spaceId string) (map[string]map[string]any, error) {
	logger.Infof("start to push all type table_id, space_type: %s, space_id: %s", spaceType, spaceId)
	// 获取数据空间记录的ID
	// NOTE: ID 需要转换为负值
	var spaceObj space.Space
	dataValues := make(map[string]map[string]any)
	if err := space.NewSpaceQuerySet(mysql.GetDBSession().DB).SpaceTypeIdEq(spaceType).SpaceIdEq(spaceId).One(&spaceObj); err != nil {
		return dataValues, err
	}

	// format: {"table_id": {"filters": [{"bk_biz_id": "-id"}]}}
	for _, tid := range models.AllSpaceTableIds {
		// dataValues[tid] = map[string]interface{}{"filters": []map[string]interface{}{{"bk_biz_id": strconv.Itoa(-spaceObj.Id)}}}

		options := FilterBuildContext{
			SpaceType:      spaceType,
			SpaceId:        spaceId,
			TableId:        tid,
			ExtraStringVal: strconv.Itoa(-spaceObj.Id),
			FilterAlias:    "bk_biz_id",
		}
		filters := s.buildFiltersByUsage(options, UsageComposeAllTypeTableIds)
		dataValues[tid] = map[string]any{"filters": filters}
	}
	// 二段式校验&补充
	dataValuesToRedis := make(map[string]map[string]any)
	for tid, values := range dataValues {
		reformattedTid := reformatTableId(tid)
		dataValuesToRedis[reformattedTid] = values
	}
	return dataValuesToRedis, nil
}

// composeApmAllTypeTableIds 组装 APM 特殊空间类型的结果表数据（仅限 bkci 和 bksaas）
func (s *SpacePusher) composeApmAllTypeTableIds(spaceType, spaceId string) (map[string]map[string]any, error) {
	logger.Infof("start to push apm all space type table_id, space_type: %s, space_id: %s", spaceType, spaceId)

	db := mysql.GetDBSession().DB

	var spaceObj space.Space
	dataValues := make(map[string]map[string]any)
	if err := space.NewSpaceQuerySet(db).SpaceTypeIdEq(spaceType).SpaceIdEq(spaceId).One(&spaceObj); err != nil {
		return dataValues, err
	}

	// 过滤包含特定字符串的结果表
	var rtList []resulttable.ResultTable
	if err := resulttable.NewResultTableQuerySet(db).
		Select(
			resulttable.ResultTableDBSchema.TableId,
			resulttable.ResultTableDBSchema.BkBizIdAlias,
		).
		BkTenantIdEq(spaceObj.BkTenantId).
		IsDeletedEq(false).
		IsEnableEq(true).
		TableIdLike("%apm_global.precalculate_storage%").
		All(&rtList); err != nil {
		return nil, err
	}

	// format: {"table_id": {"filters": [{"bk_biz_id": "-id"}]}}
	for _, rt := range rtList {
		options := FilterBuildContext{
			SpaceType:      spaceType,
			SpaceId:        spaceId,
			TableId:        rt.TableId,
			ExtraStringVal: strconv.Itoa(-spaceObj.Id),
			FilterAlias:    rt.BkBizIdAlias,
		}
		filters := s.buildFiltersByUsage(options, UsageComposeAllTypeTableIds)
		dataValues[rt.TableId] = map[string]any{"filters": filters}
	}

	// 二段式校验&补充
	dataValuesToRedis := make(map[string]map[string]any)
	for tid, values := range dataValues {
		reformattedTid := reformatTableId(tid)
		dataValuesToRedis[reformattedTid] = values
	}
	logger.Infof("compose apm all space type table_id, space_type: %s, space_id: %s, data: %v", spaceType, spaceId, dataValues)
	return dataValuesToRedis, nil
}

// SpaceRedisClearer 清理空间路由缓存
type SpaceRedisClearer struct {
	redisClient *redis.Instance
	dbClient    *gorm.DB
}

// NewSpaceRedisClearer 创建 SpaceRedisClearer 对象
func NewSpaceRedisClearer() *SpaceRedisClearer {
	return &SpaceRedisClearer{
		redisClient: redis.GetStorageRedisInstance(),
		dbClient:    mysql.GetDBSession().DB,
	}
}

// ClearSpaceToRt 清理空间路由缓存
func (s *SpaceRedisClearer) ClearSpaceToRt() {
	logger.Info("start to clear space to rt router")
	// 获取redis中所有的空间Uid
	fields, err := s.redisClient.HKeys(cfg.SpaceToResultTableKey)
	if err != nil {
		logger.Errorf("clear space to rt router, get redis key error, %s", err)
		return
	}
	// 获取真实存在的空间
	var spaceList []space.Space
	if err := space.NewSpaceQuerySet(s.dbClient).Select(space.SpaceDBSchema.SpaceTypeId, space.SpaceDBSchema.SpaceId, space.SpaceDBSchema.BkTenantId).All(&spaceList); err != nil {
		logger.Errorf("clear space to rt router, get space list error, %s", err)
		return
	}
	var spaceUidList []string
	for _, spaceObj := range spaceList {
		// 多租户模式下，需要加上租户ID后缀
		if cfg.EnableMultiTenantMode {
			spaceUidList = append(spaceUidList, fmt.Sprintf("%s__%s|%s", spaceObj.SpaceTypeId, spaceObj.SpaceId, spaceObj.BkTenantId))
		} else {
			spaceUidList = append(spaceUidList, fmt.Sprintf("%s__%s", spaceObj.SpaceTypeId, spaceObj.SpaceId))
		}
	}
	// 获取存在于redis，而不在db中的数据，然后针对key进行删除
	fieldSet := slicex.StringList2Set(fields)
	spaceUidSet := slicex.StringList2Set(spaceUidList)
	diff := fieldSet.Difference(spaceUidSet)
	// r如果长度相同，则直接返回
	if diff.Cardinality() == 0 {
		logger.Info("space to rt router, redis key is equal db records")
		return
	}
	// 批量删除
	needDeleteSpaceUidList := slicex.StringSet2List(diff)
	logger.Info("start to delete space to rt router, space_uid_list: %v", needDeleteSpaceUidList)
	if err := s.redisClient.HDel(cfg.SpaceToResultTableKey, needDeleteSpaceUidList...); err != nil {
		logger.Errorf("clear space to rt router, delete redis key error, %s", err)
		return
	}

	logger.Info("clear space to rt router success")
}

// ClearDataLabelToRt 清理数据标签路由缓存
func (s *SpaceRedisClearer) ClearDataLabelToRt() {
	logger.Info("start to clear data label to rt router")
	// 获取redis中对应的fields
	fields, err := s.redisClient.HKeys(cfg.DataLabelToResultTableKey)
	if err != nil {
		logger.Errorf("clear data label to rt router, get redis key error, %s", err)
		return
	}
	// 获取真实存在的数据标签
	var rtList []resulttable.ResultTable
	if err := resulttable.NewResultTableQuerySet(s.dbClient).Select(resulttable.ResultTableDBSchema.DataLabel, resulttable.ResultTableDBSchema.BkTenantId).DataLabelNe("").DataLabelIsNotNull().IsDeletedEq(false).IsEnableEq(true).All(&rtList); err != nil {
		logger.Errorf("clear data label to rt router, get data label list error, %s", err)
		return
	}
	var datalabelList []string
	for _, rt := range rtList {
		if rt.DataLabel != nil && *rt.DataLabel != "" {
			// 多租户模式下，需要加上租户ID后缀
			if cfg.EnableMultiTenantMode {
				datalabelList = append(datalabelList, fmt.Sprintf("%s|%s", *rt.DataLabel, rt.BkTenantId))
			} else {
				datalabelList = append(datalabelList, *rt.DataLabel)
			}
		}
	}
	// 获取存在于redis，而不在db中的数据，然后针对key进行删除
	fieldSet := slicex.StringList2Set(fields)
	dataLabelSet := slicex.StringList2Set(datalabelList)
	diff := fieldSet.Difference(dataLabelSet)
	// 如果长度相同，则直接返回
	if diff.Cardinality() == 0 {
		logger.Info("data label to rt router, redis key is equal db records")
		return
	}
	// 批量删除
	needDeleteDataLabelList := slicex.StringSet2List(diff)
	logger.Info("start to delete data label to rt router, data_label_list: %v", needDeleteDataLabelList)
	if err := s.redisClient.HDel(cfg.DataLabelToResultTableKey, needDeleteDataLabelList...); err != nil {
		logger.Errorf("clear data label to rt router, delete redis key error, %s", err)
		return
	}

	logger.Info("clear data label to rt router success")
}

// ClearRtDetail 清理结果表详情
func (s *SpaceRedisClearer) ClearRtDetail() {
	logger.Info("start to clear rt detail")
	// 获取redis中所有的rt_id
	fields, err := s.redisClient.HKeys(cfg.ResultTableDetailKey)
	if err != nil {
		logger.Errorf("clear rt detail, get redis key error, %s", err)
		return
	}
	// 获取真实存在的rt_id
	var rtList []resulttable.ResultTable
	if err := resulttable.NewResultTableQuerySet(s.dbClient).Select(resulttable.ResultTableDBSchema.TableId, resulttable.ResultTableDBSchema.BkTenantId).IsDeletedEq(false).IsEnableEq(true).All(&rtList); err != nil {
		logger.Errorf("clear rt detail, get rt list error, %s", err)
		return
	}
	var rtIdList []string
	for _, rt := range rtList {
		// 多租户模式下，需要加上租户ID后缀
		if cfg.EnableMultiTenantMode {
			rtIdList = append(rtIdList, fmt.Sprintf("%s|%s", rt.TableId, rt.BkTenantId))
		} else {
			rtIdList = append(rtIdList, rt.TableId)
		}
	}
	// 获取存在于redis，而不在db中的数据，然后针对key进行删除
	fieldSet := slicex.StringList2Set(fields)
	rtIdSet := slicex.StringList2Set(rtIdList)
	diff := fieldSet.Difference(rtIdSet)
	// 如果长度相同，则直接返回
	if diff.Cardinality() == 0 {
		logger.Info("rt detail, redis key is equal db records")
		return
	}
	// 批量删除
	needDeleteRtIdList := slicex.StringSet2List(diff)
	logger.Info("start to delete rt detail, rt_id_list: %v", needDeleteRtIdList)
	if err := s.redisClient.HDel(cfg.ResultTableDetailKey, needDeleteRtIdList...); err != nil {
		logger.Errorf("clear rt detail, delete redis key error, %s", err)
		return
	}

	logger.Info("clear rt detail success")
}
