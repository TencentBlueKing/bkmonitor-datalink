// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package task

import (
	"context"
	"fmt"
	"sync"
	"time"

	ants "github.com/panjf2000/ants/v2"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/recordrule"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	t "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	// recordRuleV4DeletedRetentionDays 保留已删除 V4 预计算表的路由窗口，避免删除后一段时间内历史查询提前 404。
	recordRuleV4DeletedRetentionDays = 180
)

// preFetchSpaceTableIds 提前获取部分空间路由信息，减少后续的查询次数
// 1. 预计算表路由 2. 短链路表路由
func preFetchSpaceTableIds(ctx context.Context, t *t.Task, spaceList []space.Space) (service.SpaceTableIdValuesBySpace, error) {
	logger.Info("start pre fetch space table ids task")

	pusher := service.NewSpacePusher()
	prefetchedValuesBySpace := make(service.SpaceTableIdValuesBySpace)

	recordRuleValuesBySpace, err := preFetchRecordRuleTableIdValues(pusher)
	if err != nil {
		return nil, err
	}
	mergeSpaceTableIdValuesBySpace(prefetchedValuesBySpace, recordRuleValuesBySpace)

	recordRuleV4ValuesBySpace, err := preFetchRecordRuleV4TableIdValues(pusher)
	if err != nil {
		return nil, err
	}
	mergeSpaceTableIdValuesBySpace(prefetchedValuesBySpace, recordRuleV4ValuesBySpace)

	shortLinkValuesBySpace, err := preFetchVMShortLinkTableIdValues(pusher, spaceList)
	if err != nil {
		return nil, err
	}
	mergeSpaceTableIdValuesBySpace(prefetchedValuesBySpace, shortLinkValuesBySpace)

	logger.Infof("pre fetch space table ids success, space_count [%d]", len(prefetchedValuesBySpace))
	return prefetchedValuesBySpace, nil
}

func preFetchRecordRuleTableIdValues(pusher *service.SpacePusher) (service.SpaceTableIdValuesBySpace, error) {
	db := mysql.GetDBSession().DB
	var recordRuleList []recordrule.RecordRule
	if err := recordrule.NewRecordRuleQuerySet(db).
		Select(
			recordrule.RecordRuleDBSchema.SpaceType,
			recordrule.RecordRuleDBSchema.SpaceId,
			recordrule.RecordRuleDBSchema.TableId,
		).
		All(&recordRuleList); err != nil {
		logger.Errorf("pre fetch record rule table ids failed, err: %s", err)
		return nil, err
	}

	valuesBySpace := pusher.ComposeRecordRuleTableIdValuesBySpace(recordRuleList)
	logger.Infof("pre fetch record rule table ids success, record_rule_count [%d], space_count [%d]", len(recordRuleList), len(valuesBySpace))
	return valuesBySpace, nil
}

func preFetchRecordRuleV4TableIdValues(pusher *service.SpacePusher) (service.SpaceTableIdValuesBySpace, error) {
	db := mysql.GetDBSession().DB
	tableName := recordrule.RecordRuleV4{}.TableName()
	if !db.HasTable(tableName) {
		logger.Warnf("pre fetch record rule v4 table ids skipped, table [%s] not exists", tableName)
		return make(service.SpaceTableIdValuesBySpace), nil
	}

	var recordRuleList []recordrule.RecordRuleV4
	queryableDeletedAt := time.Now().AddDate(0, 0, -recordRuleV4DeletedRetentionDays)
	if err := db.Unscoped().Table(tableName).
		Select("bk_tenant_id, space_type, space_id, table_id").
		Where("deleted_at IS NULL OR deleted_at > ?", queryableDeletedAt).
		Find(&recordRuleList).Error; err != nil {
		logger.Errorf("pre fetch record rule v4 table ids failed, err: %s", err)
		return nil, err
	}

	valuesBySpace := pusher.ComposeRecordRuleV4TableIdValuesBySpace(recordRuleList)
	logger.Infof("pre fetch record rule v4 table ids success, record_rule_count [%d], space_count [%d]", len(recordRuleList), len(valuesBySpace))
	return valuesBySpace, nil
}

func preFetchVMShortLinkTableIdValues(pusher *service.SpacePusher, spaceList []space.Space) (service.SpaceTableIdValuesBySpace, error) {
	db := mysql.GetDBSession().DB
	var shortLinkRecords []space.VMShortLinkRecord
	if !db.HasTable(&space.VMShortLinkRecord{}) {
		logger.Warnf("pre fetch vm short link table ids skipped, table [%s] not exists", space.VMShortLinkRecord{}.TableName())
		return make(service.SpaceTableIdValuesBySpace), nil
	}
	// 短链路路由在预取阶段一次性查出，并提前拼成 space_to_result_table 的 Redis value。
	// 下游只按 space key 合并，避免每个空间重复查询短链路记录。
	if err := db.Model(&space.VMShortLinkRecord{}).
		Select("bk_tenant_id, space_type, space_id, table_id, is_global, query_router_config").
		Where("is_enabled = ? AND is_deleted = ?", true, false).
		Find(&shortLinkRecords).Error; err != nil {
		logger.Errorf("pre fetch vm short link table ids failed, err: %s", err)
		return nil, err
	}

	valuesBySpace := pusher.ComposeVMShortLinkTableIdValuesBySpace(shortLinkRecords, spaceList)
	logger.Infof("pre fetch vm short link table ids success, record_count [%d], space_count [%d]", len(shortLinkRecords), len(valuesBySpace))
	return valuesBySpace, nil
}

func mergeSpaceTableIdValuesBySpace(dst, src service.SpaceTableIdValuesBySpace) {
	for spaceKey, values := range src {
		if _, ok := dst[spaceKey]; !ok {
			dst[spaceKey] = make(service.SpaceTableIdValues)
		}
		mergeSpaceTableIdValues(dst[spaceKey], values)
	}
}

func mergeSpaceTableIdValues(dst, src service.SpaceTableIdValues) {
	for tableId, value := range src {
		dst[tableId] = value
	}
}

// PushAndPublishSpaceRouterInfo 推送并发布空间路由信息
func PushAndPublishSpaceRouterInfo(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("PushAndPublishSpaceRouterInfo Runtime panic caught: %v", err)
		}
	}()

	var (
		wg  sync.WaitGroup
		err error
	)

	logger.Info("start push and publish space router task")
	db := mysql.GetDBSession().DB
	// 获取到所有的空间信息
	var spaceList []space.Space
	if err := space.NewSpaceQuerySet(db).All(&spaceList); err != nil {
		logger.Errorf("PushAndPublishSpaceRouterInfo get all space error, %s", err)
		return err
	}

	// 获取租户ID
	bkTenantIdSet := make(map[string]struct{})
	for _, sp := range spaceList {
		if sp.BkTenantId != "" {
			bkTenantIdSet[sp.BkTenantId] = struct{}{}
		}
	}

	// 预获取空间路由信息
	prefetchedValuesBySpace, preFetchErr := preFetchSpaceTableIds(ctx, t, spaceList)
	if preFetchErr != nil {
		logger.Errorf("PushAndPublishSpaceRouterInfo pre fetch space table ids failed, err: %s", preFetchErr)
		return preFetchErr
	}

	goroutineCount := GetGoroutineLimit("push_and_publish_space_router_info")
	pusher := service.NewSpacePusher()
	// 存放结果表数据

	t0 := time.Now()
	p, _ := ants.NewPool(goroutineCount)
	defer p.Release()

	// 处理 bk_app_to_space 关联路由
	wg.Add(1)
	_ = p.Submit(func() {
		defer wg.Done()
		t1 := time.Now()
		name := "[task] PushAndPublishSpaceRouterInfo bk_app_to_space"
		if err = pusher.PushBkAppToSpace(); err != nil {
			logger.Errorf("%s error %s", name, err)
			return
		}
		logger.Infof("%s success, cost: %s", name, time.Since(t1))
	})

	// 循环每个空间处理 space_to_result_table 空间路由数据
	for _, sp := range spaceList {
		wg.Add(1)
		sp := sp
		_ = p.Submit(func() {
			defer wg.Done()
			t1 := time.Now()
			name := fmt.Sprintf("[task] PushAndPublishSpaceRouterInfo space_to_result_table space[%s] ", sp.SpaceUid())
			prefetchedValues := make(service.SpaceTableIdValues)
			mergeSpaceTableIdValues(prefetchedValues, prefetchedValuesBySpace[service.SpaceRouteKey(sp.SpaceTypeId, sp.SpaceId)])
			tenantSpaceRouteKey := service.SpaceRouteKeyWithTenant(sp.BkTenantId, sp.SpaceTypeId, sp.SpaceId)
			if tenantSpaceRouteKey != service.SpaceRouteKey(sp.SpaceTypeId, sp.SpaceId) {
				mergeSpaceTableIdValues(prefetchedValues, prefetchedValuesBySpace[tenantSpaceRouteKey])
			}
			if err = pusher.PushSpaceTableIds(sp.BkTenantId, sp.SpaceTypeId, sp.SpaceId, prefetchedValues); err != nil {
				logger.Errorf("%s error %s", name, err)
				return
			}
			logger.Infof("%s success, cost: %s", name, time.Since(t1))
		})
	}

	// 处理 data_label_to_result_table 关联路由
	for bkTenantId := range bkTenantIdSet {
		wg.Add(1)
		bkTenantId := bkTenantId
		_ = p.Submit(func() {
			defer wg.Done()
			t1 := time.Now()
			name := fmt.Sprintf("[task] PushAndPublishSpaceRouterInfo data_label_to_result_table tenant[%s]", bkTenantId)
			if err = pusher.PushDataLabelTableIds(bkTenantId, nil, true); err != nil {
				logger.Errorf("%s error %s", name, err)
				return
			}
			logger.Infof("%s success, cost: %s", name, time.Since(t1))
		})
	}

	// 处理 result_table_detail 路由
	for bkTenantId := range bkTenantIdSet {
		wg.Add(1)
		_ = p.Submit(func() {
			defer wg.Done()
			t1 := time.Now()

			name := fmt.Sprintf("[task] PushAndPublishSpaceRouterInfo result_table_detail tenant[%s]", bkTenantId)
			var tableIdList []string
			var rtList []resulttable.ResultTable
			if err = resulttable.NewResultTableQuerySet(db).Select(resulttable.ResultTableDBSchema.TableId).BkTenantIdEq(bkTenantId).DefaultStorageIn(models.StorageTypeInfluxdb, models.StorageTypeVM).IsEnableEq(true).IsDeletedEq(false).All(&rtList); err != nil {
				logger.Errorf("%s error, %s", name, err)
				return
			}
			// 获取结果表
			for _, rt := range rtList {
				tableIdList = append(tableIdList, rt.TableId)
			}

			if err = pusher.PushTableIdDetail(bkTenantId, tableIdList, true); err != nil {
				logger.Errorf("%s error %s", name, err)
				return
			}
			logger.Infof("%s success, cost: %s", name, time.Since(t1))
		})
	}

	// 处理 result_table_detail 路由: Elasticsearch 类型
	wg.Add(1)
	_ = p.Submit(func() {
		defer wg.Done()
		t1 := time.Now()
		name := "[task] PushAndPublishSpaceRouterInfo result_table_detail (elasticsearch)"

		var tableIdList []string
		var rtList []resulttable.ResultTable

		// 查询 default_storage 为 "elasticsearch"，启用且未删除的结果表
		if err = resulttable.NewResultTableQuerySet(db).
			Select(resulttable.ResultTableDBSchema.TableId).
			DefaultStorageEq(models.StorageTypeES).
			IsEnableEq(true).IsDeletedEq(false).
			All(&rtList); err != nil {
			logger.Errorf("%s error, %s", name, err)
			return
		}

		// 提取 TableID 列表
		for _, rt := range rtList {
			tableIdList = append(tableIdList, rt.TableId)
		}

		// 调用 PushEsTableIdDetail 方法
		if err = pusher.PushEsTableIdDetail(tableIdList, true); err != nil {
			logger.Errorf("%s error %s", name, err)
			return
		}
		logger.Infof("%s success, cost: %s", name, time.Since(t1))
	})

	// 处理 result_table_detail 路由: Doris 类型
	wg.Add(1)
	_ = p.Submit(func() {
		defer wg.Done()
		t1 := time.Now()
		name := "[task] PushAndPublishSpaceRouterInfo result_table_detail (doris)"

		var tableIdList []string
		var rtList []resulttable.ResultTable

		// 查询 default_storage 为 "elasticsearch"，启用且未删除的结果表
		if err = resulttable.NewResultTableQuerySet(db).
			Select(resulttable.ResultTableDBSchema.TableId).
			DefaultStorageEq(models.StorageTypeDoris).
			IsEnableEq(true).IsDeletedEq(false).
			All(&rtList); err != nil {
			logger.Errorf("%s error, %s", name, err)
			return
		}

		// 提取 TableID 列表
		for _, rt := range rtList {
			tableIdList = append(tableIdList, rt.TableId)
		}

		// 调用 PushEsTableIdDetail 方法
		if err = pusher.PushDorisTableIdDetail(tableIdList, true); err != nil {
			logger.Errorf("%s error %s", name, err)
			return
		}
		logger.Infof("%s success, cost: %s", name, time.Since(t1))
	})

	wg.Wait()
	logger.Infof("push and publish space router successfully, cost: %s", time.Since(t0))
	return nil
}

// ClearDeprecatedRedisKey 清理过期的 redis key field
// redis key:
// - bkmonitorv3:spaces:space_to_result_table
// - bkmonitorv3:spaces:data_label_to_result_table
// - bkmonitorv3:spaces:result_table_detail
func ClearDeprecatedRedisKey(ctx context.Context, t *t.Task) error {
	logger.Info("start clear deprecated redis key field task")
	// 清理对应的key
	clearer := service.NewSpaceRedisClearer()
	clearer.ClearSpaceToRt()
	clearer.ClearDataLabelToRt()
	clearer.ClearRtDetail()

	return nil
}
