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
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	t "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// RefreshBkccSpaceName 刷新 bkcc 类型空间名称
func RefreshBkccSpaceName(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("RefreshBkccSpaceName Runtime panic caught: %v", err)
		}
	}()
	logger.Info("start sync bkcc space name task")
	svc := service.NewSpaceSvc(nil)
	if err := svc.RefreshBkccSpaceName(); err != nil {
		return errors.Wrap(err, "refresh bkcc space name failed")
	}
	logger.Info("refresh bkcc space name successfully")
	return nil
}

// RefreshClusterResource 检测集群资源的变化,当绑定资源的集群信息变动时，刷新绑定的集群资源
func RefreshClusterResource(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("RefreshClusterResource Runtime panic caught: %v", err)
		}
	}()
	logger.Infof("start sync bcs space cluster resource task")
	if err := service.NewBcsClusterInfoSvc(nil).RefreshClusterResource(); err != nil {
		logger.Errorf("sync bcs space cluster resource failed, %v", err)
		return err
	}
	logger.Infof("sync bcs space cluster resource success")
	return nil
}

// RefreshBkccSpace 同步 bkcc 的业务，自动创建对应的空间
func RefreshBkccSpace(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("RefreshBkccSpace Runtime panic caught: %v", err)
		}
	}()
	logger.Info("start sync bkcc space task")
	svc := service.NewSpaceSvc(nil)
	if err := svc.RefreshBkccSpace(false); err != nil {
		return errors.Wrap(err, "refresh bkcc space failed")
	}
	logger.Info("refresh bkcc space successfully")
	return nil
}

// SyncBkccSpaceDataSource 同步bkcc数据源和空间的关系及数据源的所属类型
func SyncBkccSpaceDataSource(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("SyncBkccSpaceDataSource Runtime panic caught: %v", err)
		}
	}()
	logger.Info("start sync bkcc space data source task")
	svc := service.NewSpaceDataSourceSvc(nil)
	if err := svc.SyncBkccSpaceDataSource(); err != nil {
		return errors.Wrap(err, "sync bkcc space data source failed")
	}
	logger.Info("sync bkcc space data source successfully")
	return nil
}

// RefreshBcsProjectBiz 检测 bcs 项目绑定的业务的变化
func RefreshBcsProjectBiz(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("RefreshBcsProjectBiz Runtime panic caught: %v", err)
		}
	}()
	logger.Info("start check and update the bind biz of bcs project task")
	svc := service.NewSpaceSvc(nil)
	if err := svc.RefreshBcsProjectBiz(); err != nil {
		return errors.Wrap(err, "refresh bcs project biz failed")
	}
	logger.Info("refresh bcs project biz successfully")
	return nil
}

// SyncBcsSpace 同步 BCS 项目空间数据
func SyncBcsSpace(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("SyncBcsSpace Runtime panic caught: %v", err)
		}
	}()
	logger.Info("start sync bcs space task")
	svc := service.NewSpaceSvc(nil)
	if err := svc.SyncBcsSpace(); err != nil {
		return errors.Wrap(err, "sync bcs space task failed")
	}
	logger.Info("sync bcs space task successfully")
	return nil
}

// RefreshBkciSpaceName 刷新 bkci 类型空间名称
func RefreshBkciSpaceName(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("RefreshBkciSpaceName Runtime panic caught: %v", err)
		}
	}()
	logger.Info("start sync bkci space name task")
	svc := service.NewSpaceSvc(nil)
	if err := svc.RefreshBkciSpaceName(); err != nil {
		return errors.Wrap(err, "refresh bkci space name failed")
	}
	logger.Info("refresh bkci space name successfully")
	return nil
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
		name := fmt.Sprintf("[task] PushAndPublishSpaceRouterInfo bk_app_to_space")
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
			name := fmt.Sprintf("[task] PushAndPublishSpaceRouterInfo space_to_result_table [%s] ", sp.SpaceUid())
			if err = pusher.PushSpaceTableIds(sp.SpaceTypeId, sp.SpaceId); err != nil {
				logger.Errorf("%s error %s", name, err)
				return
			}
			logger.Infof("%s success, cost: %s", name, time.Since(t1))
		})
	}

	// 处理 data_label_to_result_table 关联路由
	wg.Add(1)
	_ = p.Submit(func() {
		defer wg.Done()
		t1 := time.Now()
		name := fmt.Sprintf("[task] PushAndPublishSpaceRouterInfo data_label_to_result_table")
		if err = pusher.PushDataLabelTableIds(nil, nil, true); err != nil {
			logger.Errorf("%s error %s", name, err)
			return
		}
		logger.Infof("%s success, cost: %s", name, time.Since(t1))
	})

	// 处理 result_table_detail 路由
	wg.Add(1)
	_ = p.Submit(func() {
		defer wg.Done()
		t1 := time.Now()

		name := fmt.Sprintf("[task] PushAndPublishSpaceRouterInfo result_table_detail")
		var tableIdList []string
		var rtList []resulttable.ResultTable
		if err = resulttable.NewResultTableQuerySet(db).Select(resulttable.ResultTableDBSchema.TableId).DefaultStorageEq("influxdb").IsEnableEq(true).IsDeletedEq(false).All(&rtList); err != nil {
			logger.Errorf("%s error, %s", name, err)
			return
		}
		// 获取结果表
		for _, rt := range rtList {
			tableIdList = append(tableIdList, rt.TableId)
		}

		if err = pusher.PushTableIdDetail(tableIdList, true, true); err != nil {
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
