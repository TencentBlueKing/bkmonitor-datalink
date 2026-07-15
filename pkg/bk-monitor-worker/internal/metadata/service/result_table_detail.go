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
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/jinzhu/gorm"
	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// resultTableDetailBatchSize 同时约束 table_id 的 IN 查询和 Redis 批量写入；
// 单个实体表的历史分段数量不受该值限制，会在当前批次中一次加载。
const resultTableDetailBatchSize = cfg.DefaultDBFilterSize

type logRouteRefreshResult struct {
	claimedTableIDs  map[string]struct{}
	routeCount       int
	failedBatchCount int
}

// PushTableIdDetail 是 result_table_detail 的唯一刷新入口。
// tableIdList 为空时刷新租户全量路由；非空时只刷新指定结果表。
func (s *SpacePusher) PushTableIdDetail(bkTenantId string, tableIdList []string, isPublish bool) error {
	if strings.TrimSpace(bkTenantId) == "" {
		return errors.New("bk_tenant_id is required")
	}

	requestedTableIDs := uniqueSortedTableIDs(tableIdList)
	// 空 slice 表示全量；非空 slice 若只含空字符串则按无效增量请求处理，
	// 不能在清洗后意外退化为全租户刷新。
	if len(tableIdList) > 0 && len(requestedTableIDs) == 0 {
		return nil
	}

	// ClusterInfo 是租户级小表，一次加载后由日志、指标和 RecordRule 共用，
	// 避免每个 500 表批次重复查询相同集群。
	clusterMap, err := loadResultTableDetailClusterMap(mysql.GetDBSession().DB, bkTenantId)
	if err != nil {
		return errors.Wrap(err, "load result table detail clusters")
	}

	logResult, err := s.refreshLogRoutes(bkTenantId, requestedTableIDs, clusterMap, isPublish)
	if err != nil {
		return err
	}

	metricRouteCount, err := s.refreshMetricRoutes(
		bkTenantId, requestedTableIDs, logResult.claimedTableIDs, clusterMap, isPublish,
	)
	if err != nil {
		return err
	}

	logger.Infof(
		"PushTableIdDetail: completed, tenant [%s], metric_count [%d], log_count [%d], failed_log_batches [%d]",
		bkTenantId, metricRouteCount, logResult.routeCount, logResult.failedBatchCount,
	)
	return nil
}

// refreshLogRoutes 负责日志候选枚举、分批组装和容错写入，并返回日志侧所有权集合。
func (s *SpacePusher) refreshLogRoutes(
	bkTenantId string,
	requestedTableIDs []string,
	clusterMap map[uint]storage.ClusterInfo,
	isPublish bool,
) (logRouteRefreshResult, error) {
	logTableIDs, err := s.listLogTableIDs(bkTenantId, requestedTableIDs)
	if err != nil {
		return logRouteRefreshResult{}, errors.Wrap(err, "list log result tables")
	}

	result := logRouteRefreshResult{
		claimedTableIDs: make(map[string]struct{}, len(logTableIDs)),
	}
	for _, batchTableIDs := range chunkResultTableIDs(logTableIDs) {
		batchDetails, batchClaimed, composeErr := s.composeLogTableIdDetail(
			bkTenantId, batchTableIDs, clusterMap,
		)
		if composeErr != nil {
			return result, errors.Wrapf(composeErr, "compose log route batch for tenant [%s]", bkTenantId)
		}
		for tableID := range batchClaimed {
			result.claimedTableIDs[tableID] = struct{}{}
		}

		if len(batchDetails) == 0 {
			continue
		}
		if writeErr := s.writeTableIdDetail(bkTenantId, batchDetails, isPublish, true); writeErr != nil {
			// 延续日志路由后处理的容错：单批 Redis 写失败不阻断其他日志批次和指标路由。
			logger.Errorf("PushTableIdDetail: write log route batch failed, tenant [%s], first_table [%s], error [%s]", bkTenantId, batchTableIDs[0], writeErr)
			result.failedBatchCount++
			continue
		}
		result.routeCount += len(batchDetails)
	}
	return result, nil
}

// refreshMetricRoutes 负责排除日志侧已认领的表，并保持普通指标与 RecordRule 的旧覆盖语义。
func (s *SpacePusher) refreshMetricRoutes(
	bkTenantId string,
	requestedTableIDs []string,
	claimedTableIDs map[string]struct{},
	clusterMap map[uint]storage.ClusterInfo,
	isPublish bool,
) (int, error) {
	metricTableIDs, err := s.listMetricTableIDs(bkTenantId, requestedTableIDs)
	if err != nil {
		return 0, errors.Wrap(err, "list metric result tables")
	}
	metricTableIDs = excludeClaimedTableIDs(metricTableIDs, claimedTableIDs)

	// 全量刷新即使只有 RecordRule 也必须写入；增量刷新只在实际组装出
	// AccessVMRecord 路由时附带该租户的全部 RecordRule。
	shouldRefreshRecordRules := len(requestedTableIDs) == 0

	metricRouteCount := 0
	recordRulesLoaded := false
	recordRuleDetails := make(map[string]map[string]any)
	loadRecordRules := func() error {
		if recordRulesLoaded {
			return nil
		}
		var composeErr error
		recordRuleDetails, composeErr = s.composeRecordRuleTableIdDetail(bkTenantId, clusterMap)
		if composeErr != nil {
			return errors.Wrapf(composeErr, "compose record rule route for tenant [%s]", bkTenantId)
		}
		recordRulesLoaded = true
		return nil
	}
	for _, batchTableIDs := range chunkResultTableIDs(metricTableIDs) {
		batchDetails, composeErr := s.composeMetricTableIdDetail(
			bkTenantId, batchTableIDs, clusterMap,
		)
		if composeErr != nil {
			return 0, errors.Wrapf(composeErr, "compose metric route batch for tenant [%s]", bkTenantId)
		}
		if len(batchDetails) > 0 {
			shouldRefreshRecordRules = true
			if loadErr := loadRecordRules(); loadErr != nil {
				return 0, loadErr
			}
			// 保持原入口的覆盖语义：同名 RecordRule payload 覆盖普通指标 payload，
			// 并在本批一次写入，避免同一 field 重复 HSET/PUBLISH。
			for tableID := range batchDetails {
				if recordRuleDetail, exists := recordRuleDetails[tableID]; exists {
					batchDetails[tableID] = recordRuleDetail
					delete(recordRuleDetails, tableID)
				}
			}
		}
		if len(batchDetails) == 0 {
			continue
		}
		if writeErr := s.writeTableIdDetail(bkTenantId, batchDetails, isPublish, false); writeErr != nil {
			return 0, errors.Wrapf(writeErr, "write metric route batch for tenant [%s]", bkTenantId)
		}
		metricRouteCount += len(batchDetails)
	}

	if !shouldRefreshRecordRules {
		return metricRouteCount, nil
	}
	if !recordRulesLoaded {
		if loadErr := loadRecordRules(); loadErr != nil {
			return 0, loadErr
		}
	}
	for tableID := range recordRuleDetails {
		if _, isClaimed := claimedTableIDs[tableID]; isClaimed {
			delete(recordRuleDetails, tableID)
		}
	}
	for _, batchDetails := range chunkTableIDDetails(recordRuleDetails, resultTableDetailBatchSize) {
		if writeErr := s.writeTableIdDetail(bkTenantId, batchDetails, isPublish, false); writeErr != nil {
			return 0, errors.Wrapf(writeErr, "write record rule route batch for tenant [%s]", bkTenantId)
		}
		metricRouteCount += len(batchDetails)
	}
	return metricRouteCount, nil
}

// listLogTableIDs 在增量刷新时原样交回请求列表，由组装阶段判断是否属于日志路由；
// 全量刷新时只枚举当前租户启用且未删除的日志 RT。
func (s *SpacePusher) listLogTableIDs(bkTenantId string, requestedTableIDs []string) ([]string, error) {
	if len(requestedTableIDs) > 0 {
		return requestedTableIDs, nil
	}

	db := mysql.GetDBSession().DB
	tableIDSet := make(map[string]struct{})
	// 候选条件直接下推到数据库，只加载后续日志组装需要的 table_id。
	var resultTableList []resulttable.ResultTable
	if err := resulttable.NewResultTableQuerySet(db).
		Select(resulttable.ResultTableDBSchema.TableId).
		BkTenantIdEq(bkTenantId).
		DefaultStorageIn(models.StorageTypeES, models.StorageTypeDoris).
		IsDeletedEq(false).
		IsEnableEq(true).
		All(&resultTableList); err != nil {
		return nil, err
	}
	for _, item := range resultTableList {
		tableIDSet[item.TableId] = struct{}{}
	}

	return sortedTableIDSet(tableIDSet), nil
}

// listMetricTableIDs 在增量刷新时原样交回请求列表；全量刷新时只枚举当前租户的
// AccessVMRecord，并兼容 ResultTable.default_storage 为 influxdb 的指标表。
func (s *SpacePusher) listMetricTableIDs(bkTenantId string, requestedTableIDs []string) ([]string, error) {
	if len(requestedTableIDs) > 0 {
		return requestedTableIDs, nil
	}

	db := mysql.GetDBSession().DB
	tableIDSet := make(map[string]struct{})
	var vmRecordList []storage.AccessVMRecord
	if err := storage.NewAccessVMRecordQuerySet(db).
		Select(storage.AccessVMRecordDBSchema.ResultTableId).
		BkTenantIdEq(bkTenantId).
		All(&vmRecordList); err != nil {
		return nil, err
	}
	for _, item := range vmRecordList {
		tableIDSet[item.ResultTableId] = struct{}{}
	}

	return sortedTableIDSet(tableIDSet), nil
}

// composeLogTableIdDetail 返回当前批次可写入 Redis 的日志 payload，以及日志侧所有权集合 claimed。
// claimed 故意可能大于 payload：default_storage 已声明为 ES/Doris 的残缺 RT 仍归日志侧所有，
// 这样配置缺失时只跳过写入，不会错误回退成指标路由覆盖旧缓存。
func (s *SpacePusher) composeLogTableIdDetail(
	bkTenantId string, tableIDs []string, clusterMap map[uint]storage.ClusterInfo,
) (map[string]map[string]any, map[string]struct{}, error) {
	result := make(map[string]map[string]any)
	claimed := make(map[string]struct{})
	if len(tableIDs) == 0 {
		return result, claimed, nil
	}

	db := mysql.GetDBSession().DB
	// 1. 先批量加载请求表自身的 RT。ResultTable 是日志路由的入口；没有同租户 RT
	// 的 Storage 不参与刷新，也不需要继续查询其存储配置。
	rtMap, err := loadResultTableMap(db, bkTenantId, tableIDs)
	if err != nil {
		return nil, nil, err
	}

	// 2. 先确定日志侧所有权，再筛出需要组装的有效日志 RT。claimed 与 result
	// 相互独立：配置残缺或生命周期失效的日志 RT 不会回退为指标路由。
	logTableIDs := make([]string, 0, len(tableIDs))
	for _, tableID := range tableIDs {
		rt, exists := rtMap[tableID]
		if !exists || (rt.DefaultStorage != models.StorageTypeES && rt.DefaultStorage != models.StorageTypeDoris) {
			continue
		}
		claimed[tableID] = struct{}{}
		if !rt.IsDeleted && rt.IsEnable {
			logTableIDs = append(logTableIDs, tableID)
		}
	}
	logTableIDs = uniqueSortedTableIDs(logTableIDs)
	if len(logTableIDs) == 0 {
		return result, claimed, nil
	}

	// 3. Storage 查询只覆盖有效日志 RT；虚拟表的 origin 实体配置稍后按需补齐。
	esMap, err := loadESStorageMap(db, bkTenantId, logTableIDs)
	if err != nil {
		return nil, nil, err
	}
	dorisMap, err := loadDorisStorageMap(db, bkTenantId, logTableIDs)
	if err != nil {
		return nil, nil, err
	}

	// 4. 为每张候选表确定顶层存储和历史分段来源：顶层存储只服从
	// ResultTable.default_storage；虚拟表沿 origin 读取历史。
	selectedStorageType := make(map[string]string)
	recordSourceTableID := make(map[string]string)
	for _, tableID := range logTableIDs {
		rt := rtMap[tableID]
		storageType := rt.DefaultStorage
		switch storageType {
		case models.StorageTypeES:
			if _, exists := esMap[tableID]; !exists {
				logger.Errorf("compose log detail: default ES storage missing, tenant [%s], table_id [%s]", bkTenantId, tableID)
				continue
			}
		case models.StorageTypeDoris:
			if _, exists := dorisMap[tableID]; !exists {
				logger.Errorf("compose log detail: default Doris storage missing, tenant [%s], table_id [%s]", bkTenantId, tableID)
				continue
			}
		default:
			continue
		}

		selectedStorageType[tableID] = storageType
		// 虚拟表优先沿当前顶层存储的 origin 找实体表；另一类 Storage 的 origin
		// 仅作为迁移期兼容 fallback，不能反向改变顶层 storageType。
		selectedOrigin := ""
		fallbackOrigin := ""
		if storageType == models.StorageTypeES {
			selectedOrigin = esMap[tableID].OriginTableId
			fallbackOrigin = dorisMap[tableID].OriginTableId
		} else {
			selectedOrigin = dorisMap[tableID].OriginTableId
			fallbackOrigin = esMap[tableID].OriginTableId
		}
		sourceTableID := selectedOrigin
		if sourceTableID == "" {
			sourceTableID = fallbackOrigin
		}
		if sourceTableID == "" {
			sourceTableID = tableID
		}
		recordSourceTableID[tableID] = sourceTableID
	}

	if len(selectedStorageType) == 0 {
		return result, claimed, nil
	}

	// 5. 批量补齐 origin 实体表及组装所需的 option、alias。target 已在首轮
	// 加载，只查询批次外的 origin，避免重复 SQL。
	sourceTableIDs := sortedStringMapValues(recordSourceTableID)
	// 初始批次已经同时查询过三类 target 表；source 与 target 相同时，map 中缺失
	// 代表数据库中确实不存在，不能再按 origin 重复查询。只加载批次外的实体表。
	originTableIDs := excludeKnownTableIDs(sourceTableIDs, logTableIDs)
	originRTMap, err := loadResultTableMap(db, bkTenantId, originTableIDs)
	if err != nil {
		return nil, nil, err
	}
	originESMap, err := loadESStorageMap(db, bkTenantId, originTableIDs)
	if err != nil {
		return nil, nil, err
	}
	originDorisMap, err := loadDorisStorageMap(db, bkTenantId, originTableIDs)
	if err != nil {
		return nil, nil, err
	}

	selectedTableIDs := sortedStringMapKeys(selectedStorageType)
	optionsMap, err := loadResultTableOptions(db, bkTenantId, selectedTableIDs)
	if err != nil {
		return nil, nil, err
	}
	aliasTableIDs := uniqueSortedTableIDs(append(append([]string{}, selectedTableIDs...), sourceTableIDs...))
	fieldAliasMap, err := s.getFieldAliasMap(bkTenantId, aliasTableIDs)
	if err != nil {
		return nil, nil, err
	}

	// 6. 按 source 表加载全部有效历史分段，并在内存中稳定排序。ClusterInfo
	// 已在租户刷新入口一次加载，当前批次只按 ID 从只读 map 取值。
	var storageRecords []storage.ClusterRecord
	// 历史分段按 origin 实体表读取全部未删除记录，再在内存中用完整时间精度和 ID 排序。
	// queryset 未重生成，因此 ID 通过字段类型字面量选择，仅用于相同 enable_time 时稳定定序。
	if err := storage.NewClusterRecordQuerySet(db).
		Select(
			storage.ClusterRecordDBSchemaField("id"),
			storage.ClusterRecordDBSchema.TableID,
			storage.ClusterRecordDBSchema.ClusterID,
			storage.ClusterRecordDBSchema.EnableTime,
		).
		BkTenantIDEq(bkTenantId).
		TableIDIn(sourceTableIDs...).
		IsDeletedEq(false).
		All(&storageRecords); err != nil {
		return nil, nil, err
	}
	recordsByTableID := make(map[string][]storage.ClusterRecord)
	for _, record := range storageRecords {
		recordsByTableID[record.TableID] = append(recordsByTableID[record.TableID], record)
	}
	for tableID := range recordsByTableID {
		sortStorageClusterRecords(recordsByTableID[tableID])
	}

	// 7. 逐表组装 payload：target 决定当前存储，origin 补充 db/source_type
	// 等实体配置；残缺历史段单独跳过，当前存储不完整则整张表跳过。
	for tableID, storageType := range selectedStorageType {
		sourceTableID := recordSourceTableID[tableID]
		targetES := esMap[tableID]
		originES := esMap[sourceTableID]
		if originES.TableID == "" {
			originES = originESMap[sourceTableID]
		}
		targetDoris := dorisMap[tableID]
		originDoris := dorisMap[sourceTableID]
		if originDoris.TableID == "" {
			originDoris = originDorisMap[sourceTableID]
		}

		esDB := firstNonEmpty(targetES.IndexSet, originES.IndexSet)
		esSourceType := firstNonEmpty(targetES.SourceType, originES.SourceType)
		dorisDB := firstNonEmpty(targetDoris.BkbaseTableID, originDoris.BkbaseTableID)

		// ClusterRecord 只记录集群与启用时间，db/source_type 等配置取自 origin 实体表。
		// payload 只输出 enable_time；UQ 用下一分段的 enable_time 推导区间结束点，
		// 因此这里有意不输出 disable_time。
		history := make([]map[string]any, 0, len(recordsByTableID[sourceTableID]))
		for _, record := range recordsByTableID[sourceTableID] {
			// 单个历史分段配置不完整时只跳过该分段，不能让它影响当前顶层路由。
			cluster, exists := clusterMap[uint(record.ClusterID)]
			if !exists {
				logger.Warnf("compose log history: cluster missing or tenant mismatch, tenant [%s], table_id [%s], record_id [%d]", bkTenantId, tableID, record.ID)
				continue
			}
			switch cluster.ClusterType {
			case models.StorageTypeES:
				if esDB == "" || esSourceType == "" {
					logger.Warnf("compose log history: ES storage config missing, tenant [%s], table_id [%s], record_id [%d]", bkTenantId, tableID, record.ID)
					continue
				}
				history = append(history, map[string]any{
					"storage_id": record.ClusterID, "storage_type": models.StorageTypeES,
					"db": esDB, "measurement": models.TSGroupDefaultMeasurement,
					"source_type": esSourceType, "enable_time": storageRecordEnableTimestamp(record),
				})
			case models.StorageTypeDoris:
				if dorisDB == "" {
					logger.Warnf("compose log history: Doris storage config missing, tenant [%s], table_id [%s], record_id [%d]", bkTenantId, tableID, record.ID)
					continue
				}
				history = append(history, map[string]any{
					"storage_id": record.ClusterID, "storage_type": models.StorageTypeBkSql,
					"storage_name": cluster.ClusterName, "cluster_name": cluster.ClusterName,
					"db": dorisDB, "measurement": models.DorisMeasurement,
					"enable_time": storageRecordEnableTimestamp(record),
				})
			}
		}

		originRT := rtMap[sourceTableID]
		if originRT.TableId == "" {
			originRT = originRTMap[sourceTableID]
		}
		dataLabel, labels := resultTableRouteMeta(rtMap[tableID], originRT)
		fieldAlias := fieldAliasMap[tableID]
		if len(fieldAlias) == 0 {
			fieldAlias = fieldAliasMap[sourceTableID]
		}
		if fieldAlias == nil {
			fieldAlias = map[string]string{}
		}

		switch storageType {
		case models.StorageTypeES:
			storageID := targetES.StorageClusterID
			cluster, exists := clusterMap[storageID]
			if !exists || cluster.ClusterType != models.StorageTypeES || esDB == "" || esSourceType == "" {
				logger.Errorf("compose log detail: ES config incomplete, tenant [%s], table_id [%s], cluster_id [%d]", bkTenantId, tableID, storageID)
				continue
			}
			result[tableID] = map[string]any{
				"storage_type": models.StorageTypeES, "storage_id": storageID,
				"db": esDB, "measurement": models.TSGroupDefaultMeasurement, "source_type": esSourceType,
				"options": optionsMap[tableID], "storage_cluster_records": history,
				"data_label": dataLabel, "labels": labels, "field_alias": fieldAlias,
			}
		case models.StorageTypeDoris:
			storageID := targetDoris.StorageClusterID
			cluster, exists := clusterMap[storageID]
			if !exists || cluster.ClusterType != models.StorageTypeDoris || dorisDB == "" {
				logger.Errorf("compose log detail: Doris config incomplete, tenant [%s], table_id [%s], cluster_id [%d]", bkTenantId, tableID, storageID)
				continue
			}
			result[tableID] = map[string]any{
				"storage_type": models.StorageTypeBkSql, "storage_id": storageID,
				"storage_name": cluster.ClusterName, "cluster_name": cluster.ClusterName,
				"db": dorisDB, "measurement": models.DorisMeasurement,
				"storage_cluster_records": history, "data_label": dataLabel,
				"labels": labels, "field_alias": fieldAlias,
			}
		}
	}

	return result, claimed, nil
}

// writeTableIdDetail 集中处理 key 规范化、租户后缀、稳定 JSON 编码和批量 compare/publish。
// normalizeTableID 只为日志路由启用：一段式 ID 补 .__default__；指标和 RecordRule 保持原 ID。
func (s *SpacePusher) writeTableIdDetail(
	bkTenantId string, tableIDDetails map[string]map[string]any, isPublish, normalizeTableID bool,
) error {
	if len(tableIDDetails) == 0 {
		return nil
	}

	redisValues := make(map[string]string, len(tableIDDetails))
	for tableID, detail := range tableIDDetails {
		redisTableID := tableID
		if normalizeTableID {
			var valid bool
			redisTableID, valid = normalizeLogTableID(tableID)
			if !valid {
				logger.Errorf("write log detail: invalid table_id, tenant [%s], table_id [%s]", bkTenantId, tableID)
				continue
			}
		}
		redisTableID = composeTenantRedisKey(redisTableID, bkTenantId)
		// encoding/json 会稳定排序 map key，避免 sonic 的随机 map 顺序让大批量
		// 刷新反复触发 Redis 侧的完整 JSON 语义比较。
		valueBytes, err := json.Marshal(detail)
		if err != nil {
			return errors.Wrapf(err, "marshal result_table_detail for table [%s]", tableID)
		}
		redisValues[redisTableID] = string(valueBytes)
	}
	if len(redisValues) == 0 {
		return nil
	}

	_, err := redis.GetStorageRedisInstance().HSetManyWithCompareAndPublish(
		cfg.ResultTableDetailKey, redisValues, cfg.ResultTableDetailChannel, isPublish,
	)
	return err
}

func loadResultTableMap(db *gorm.DB, bkTenantId string, tableIDs []string) (map[string]resulttable.ResultTable, error) {
	result := make(map[string]resulttable.ResultTable, len(tableIDs))
	if len(tableIDs) == 0 {
		return result, nil
	}
	var rows []resulttable.ResultTable
	if err := resulttable.NewResultTableQuerySet(db).
		Select(
			resulttable.ResultTableDBSchema.TableId,
			resulttable.ResultTableDBSchema.DefaultStorage,
			resulttable.ResultTableDBSchema.DataLabel,
			resulttable.ResultTableDBSchema.Labels,
			resulttable.ResultTableDBSchema.IsDeleted,
			resulttable.ResultTableDBSchema.IsEnable,
		).
		BkTenantIdEq(bkTenantId).
		TableIdIn(tableIDs...).
		All(&rows); err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.TableId] = row
	}
	return result, nil
}

func loadESStorageMap(db *gorm.DB, bkTenantId string, tableIDs []string) (map[string]storage.ESStorage, error) {
	result := make(map[string]storage.ESStorage, len(tableIDs))
	if len(tableIDs) == 0 {
		return result, nil
	}
	var rows []storage.ESStorage
	if err := storage.NewESStorageQuerySet(db).
		Select(
			storage.ESStorageDBSchema.TableID,
			storage.ESStorageDBSchema.StorageClusterID,
			storage.ESStorageDBSchema.SourceType,
			storage.ESStorageDBSchema.IndexSet,
			storage.ESStorageDBSchema.OriginTableId,
		).
		BkTenantIDEq(bkTenantId).
		TableIDIn(tableIDs...).
		All(&rows); err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.TableID] = row
	}
	return result, nil
}

func loadDorisStorageMap(db *gorm.DB, bkTenantId string, tableIDs []string) (map[string]storage.DorisStorage, error) {
	result := make(map[string]storage.DorisStorage, len(tableIDs))
	if len(tableIDs) == 0 {
		return result, nil
	}
	var rows []storage.DorisStorage
	if err := storage.NewDorisStorageQuerySet(db).
		Select(
			storage.DorisStorageDBSchema.TableID,
			storage.DorisStorageDBSchema.StorageClusterID,
			storage.DorisStorageDBSchema.BkbaseTableID,
			storage.DorisStorageDBSchema.OriginTableId,
		).
		BkTenantIDEq(bkTenantId).
		TableIDIn(tableIDs...).
		All(&rows); err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.TableID] = row
	}
	return result, nil
}

func loadResultTableOptions(
	db *gorm.DB, bkTenantId string, tableIDs []string,
) (map[string]map[string]any, error) {
	result := make(map[string]map[string]any, len(tableIDs))
	for _, tableID := range tableIDs {
		result[tableID] = map[string]any{}
	}
	if len(tableIDs) == 0 {
		return result, nil
	}

	var rows []resulttable.ResultTableOption
	if err := resulttable.NewResultTableOptionQuerySet(db).
		Select(
			resulttable.ResultTableOptionDBSchema.TableID,
			resulttable.ResultTableOptionDBSchema.Name,
			resulttable.ResultTableOptionDBSchema.Value,
			resulttable.ResultTableOptionDBSchema.ValueType,
		).
		BkTenantIdEq(bkTenantId).
		TableIDIn(tableIDs...).
		All(&rows); err != nil {
		return nil, err
	}
	for index := range rows {
		value, err := rows[index].InterfaceValue()
		if err != nil {
			logger.Warnf("compose log detail: invalid result table option skipped, tenant [%s], table_id [%s], option [%s], error [%s]", bkTenantId, rows[index].TableID, rows[index].Name, err)
			continue
		}
		result[rows[index].TableID][rows[index].Name] = value
	}
	return result, nil
}

// loadResultTableDetailClusterMap 一次加载当前租户全部 ClusterInfo。
func loadResultTableDetailClusterMap(
	db *gorm.DB, bkTenantId string,
) (map[uint]storage.ClusterInfo, error) {
	var rows []storage.ClusterInfo
	if err := storage.NewClusterInfoQuerySet(db).
		Select(
			storage.ClusterInfoDBSchema.ClusterID,
			storage.ClusterInfoDBSchema.ClusterName,
			storage.ClusterInfoDBSchema.ClusterType,
		).
		BkTenantIDEq(bkTenantId).
		All(&rows); err != nil {
		return nil, err
	}

	result := make(map[uint]storage.ClusterInfo, len(rows))
	for _, row := range rows {
		result[row.ClusterID] = row
	}
	return result, nil
}

// resultTableRouteMeta 优先保留当前（可能是虚拟）RT 的元数据，仅在空值时回退 origin，
// 并保证返回的 labels 始终是非 nil map。
func resultTableRouteMeta(current, origin resulttable.ResultTable) (string, map[string]any) {
	dataLabel := resultTableDataLabel(current.DataLabel)
	if dataLabel == "" {
		dataLabel = resultTableDataLabel(origin.DataLabel)
	}
	labels := normalizeResultTableLabels(current.TableId, current.Labels)
	if len(labels) == 0 {
		labels = normalizeResultTableLabels(origin.TableId, origin.Labels)
	}
	if labels == nil {
		labels = map[string]any{}
	}
	return dataLabel, labels
}

// storageRecordEnableTimestamp 将历史兼容的空启用时间统一映射为 Unix 0。
func storageRecordEnableTimestamp(record storage.ClusterRecord) int64 {
	if record.EnableTime == nil {
		return 0
	}
	return record.EnableTime.Unix()
}

// sortStorageClusterRecords 按 (enable_time, ID) DESC 排序，空时间按 Unix 0 处理。
// 不按 cluster 去重，A→B→A 等重复切换必须完整保留；ID 只用于相同时间的确定性顺序。
func sortStorageClusterRecords(records []storage.ClusterRecord) {
	sort.SliceStable(records, func(i, j int) bool {
		leftTime := time.Unix(0, 0)
		if records[i].EnableTime != nil {
			leftTime = *records[i].EnableTime
		}
		rightTime := time.Unix(0, 0)
		if records[j].EnableTime != nil {
			rightTime = *records[j].EnableTime
		}
		if leftTime.Equal(rightTime) {
			return records[i].ID > records[j].ID
		}
		return leftTime.After(rightTime)
	})
}

// normalizeLogTableID 将一段式日志表补成 Redis consumer 使用的 .__default__ 形式；
// 已经超过两段的 ID 无法安全判断语义，直接视为无效。
func normalizeLogTableID(tableID string) (string, bool) {
	if tableID == "" {
		return "", false
	}
	parts := strings.Split(tableID, ".")
	switch len(parts) {
	case 1:
		return tableID + ".__default__", true
	case 2:
		return tableID, true
	default:
		return "", false
	}
}

func chunkTableIDDetails(details map[string]map[string]any, batchSize int) []map[string]map[string]any {
	if len(details) == 0 {
		return nil
	}
	keys := make([]string, 0, len(details))
	for tableID := range details {
		keys = append(keys, tableID)
	}
	sort.Strings(keys)
	result := make([]map[string]map[string]any, 0, (len(keys)+batchSize-1)/batchSize)
	for start := 0; start < len(keys); start += batchSize {
		end := start + batchSize
		if end > len(keys) {
			end = len(keys)
		}
		batch := make(map[string]map[string]any, end-start)
		for _, tableID := range keys[start:end] {
			batch[tableID] = details[tableID]
		}
		result = append(result, batch)
	}
	return result
}

func chunkResultTableIDs(tableIDs []string) [][]string {
	return slicex.ChunkSlice(tableIDs, resultTableDetailBatchSize)
}

func uniqueSortedTableIDs(tableIDs []string) []string {
	set := make(map[string]struct{}, len(tableIDs))
	for _, tableID := range tableIDs {
		if tableID != "" {
			set[tableID] = struct{}{}
		}
	}
	return sortedTableIDSet(set)
}

// excludeClaimedTableIDs 排除已经归日志侧所有的表，避免指标路由覆盖日志路由。
func excludeClaimedTableIDs(tableIDs []string, claimed map[string]struct{}) []string {
	if len(tableIDs) == 0 || len(claimed) == 0 {
		return tableIDs
	}

	result := make([]string, 0, len(tableIDs))
	for _, tableID := range tableIDs {
		if _, exists := claimed[tableID]; !exists {
			result = append(result, tableID)
		}
	}
	return result
}

func sortedTableIDSet(set map[string]struct{}) []string {
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func sortedStringMapKeys[V any](values map[string]V) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func sortedStringMapValues(values map[string]string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return sortedTableIDSet(set)
}

func excludeKnownTableIDs(tableIDs, knownTableIDs []string) []string {
	known := make(map[string]struct{}, len(knownTableIDs))
	for _, tableID := range knownTableIDs {
		known[tableID] = struct{}{}
	}
	result := make([]string, 0, len(tableIDs))
	for _, tableID := range tableIDs {
		if _, exists := known[tableID]; !exists {
			result = append(result, tableID)
		}
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
