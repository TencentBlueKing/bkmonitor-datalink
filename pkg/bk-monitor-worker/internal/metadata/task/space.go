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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	t "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

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
			if err = pusher.PushSpaceTableIds(sp.BkTenantId, sp.SpaceTypeId, sp.SpaceId); err != nil {
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
