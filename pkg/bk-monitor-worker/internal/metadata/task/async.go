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

	"github.com/jinzhu/gorm"
	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/dependentredis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// CreateEsStorageIndexParams CreateEsStorageIndex 任务入参
type CreateEsStorageIndexParams struct {
	TableId string `json:"table_id"`
}

// CreateEsStorageIndex 异步创建es索引
func CreateEsStorageIndex(ctx context.Context, t *task.Task) error {
	var params CreateEsStorageIndexParams
	if err := jsonx.Unmarshal(t.Payload, &params); err != nil {
		return errors.Wrap(err, fmt.Sprintf("parse params error, %s", err))
	}
	if params.TableId == "" {
		return errors.New("params table_id can not be empty")
	}
	logger.Infof("table_id: %s start to create es index", params.TableId)

	var esStorage storage.ESStorage
	if err := storage.NewESStorageQuerySet(mysql.GetDBSession().DB).TableIDEq(params.TableId).One(&esStorage); err != nil {
		if gorm.IsRecordNotFoundError(err) {
			logger.Infof("query ESStorage by table_id [%s] not exist", params.TableId)
			return nil
		}
		return err
	}
	svc := service.NewEsStorageSvc(&esStorage)
	exist, err := svc.CheckIndexExist(ctx)
	if err != nil {
		return err
	}
	if !exist {
		if err := svc.CreateIndexAndAliases(ctx, svc.SliceGap); err != nil {
			return err
		}
	} else {
		if err := svc.UpdateIndexAndAliases(ctx, svc.SliceGap); err != nil {
			return err
		}
	}
	// 创建完 ES 相关配置后，需要刷新consul
	var rt resulttable.ResultTable
	if err := resulttable.NewResultTableQuerySet(mysql.GetDBSession().DB).TableIdEq(params.TableId).One(&rt); err != nil {
		if gorm.IsRecordNotFoundError(err) {
			logger.Errorf("query ResultTable by table_id [%s] not exist ", params.TableId)
		}
		return err
	}
	if err := service.NewResultTableSvc(&rt).RefreshEtlConfig(); err != nil {
		return errors.Wrapf(err, "refresh etl config for table_id [%s] failed, %v", params.TableId, err)
	}

	logger.Infof("table_id [%s] create es index finished", params.TableId)
	return nil
}

// AccessBkdataVmParams AccessBkdataVm 任务入参
type AccessBkdataVmParams struct {
	BkBizId  int    `json:"bk_biz_id"`
	TableId  string `json:"table_id"`
	BkDataId uint   `json:"data_id"`
}

// AccessBkdataVm 接入计算平台 VM 任务
func AccessBkdataVm(ctx context.Context, t *task.Task) error {
	var params AccessBkdataVmParams
	if err := jsonx.Unmarshal(t.Payload, &params); err != nil {
		return errors.Wrap(err, fmt.Sprintf("parse params error, %s", err))
	}
	logger.Infof("bk_biz_id [%v] table_id [%s] data_id [%v] start access bkdata vm", params.BkBizId, params.TableId, params.BkDataId)
	if err := service.NewVmUtils().AccessBkdata(params.BkBizId, params.TableId, params.BkDataId); err != nil {
		return fmt.Errorf("bk_biz_id [%v] table_id [%s] data_id [%v] start access bkdata vm failed, %v", params.BkBizId, params.TableId, params.BkDataId, err)
	}
	logger.Infof("bk_biz_id [%v] table_id [%s] data_id [%v] finish access bkdata vm", params.BkBizId, params.TableId, params.BkDataId)
	return nil
}

// PushAndPublishSpaceRouterParams PushAndPublishSpaceRouter 任务入参
type PushAndPublishSpaceRouterParams struct {
	SpaceType   string   `json:"space_type"`
	SpaceId     string   `json:"space_id"`
	TableIdList []string `json:"table_id_list"`
}

// PushAndPublishSpaceRouter 推送并发布空间路由功能
func PushAndPublishSpaceRouter(ctx context.Context, t *task.Task) error {
	var params PushAndPublishSpaceRouterParams
	if err := jsonx.Unmarshal(t.Payload, &params); err != nil {
		return errors.Wrap(err, fmt.Sprintf("parse params error, %s", err))
	}

	logger.Infof("start to push and publish space_type [%s], space_id [%s] router", params.SpaceType, params.SpaceId)
	svc := service.NewSpaceRedisSvc(GetGoroutineLimit("push_and_publish_space_router"))
	// 获取空间下的结果表，如果不存在，则获取空间下的所有
	if len(params.TableIdList) == 0 {
		tableDataIdMap, err := svc.GetSpaceTableIdDataId(params.SpaceType, params.SpaceId, nil, nil, nil)
		if err != nil {
			return errors.Wrapf(err, "get space table id dataid failed, %v", err)
		}
		for tableId := range tableDataIdMap {
			params.TableIdList = append(params.TableIdList, tableId)
		}
	}

	// 更新数据
	pusher := service.NewSpacePusherV2()
	if err := pusher.PushDataLabelTableIds(nil, params.TableIdList, true); err != nil {
		return err
	}
	if err := pusher.PushTableIdDetail(params.TableIdList, true); err != nil {
		return err
	}

	// 更新空间下的结果表相关数据
	db := mysql.GetDBSession().DB
	if params.SpaceType != "" && params.SpaceId != "" {
		// 更新相关数据到 redis
		if err := pusher.PushSpaceTableIds(params.SpaceType, params.SpaceId, true); err != nil {
			return err
		}
	} else {
		// NOTE: 现阶段仅针对 bkcc 类型做处理
		var spList []space.Space
		if err := space.NewSpaceQuerySet(db).SpaceTypeIdEq(models.SpaceTypeBKCC).Select(space.SpaceDBSchema.SpaceId).All(&spList); err != nil {
			return err
		}
		wg := &sync.WaitGroup{}
		ch := make(chan bool, GetGoroutineLimit("push_and_publish_space_router"))
		wg.Add(len(spList))
		for _, sp := range spList {
			ch <- true
			go func(sp space.Space, wg *sync.WaitGroup, ch chan bool) {
				defer func() {
					<-ch
					wg.Done()
				}()
				if err := pusher.PushSpaceTableIds(models.SpaceTypeBKCC, sp.SpaceId, false); err != nil {
					logger.Errorf("push space [%s__%s] to redis error, %s", models.SpaceTypeBKCC, sp.SpaceTypeId, err)
				} else {
					logger.Infof("push space [%s__%s] to redis success", models.SpaceTypeBKCC, sp.SpaceTypeId)
				}
				return
			}(sp, wg, ch)
		}
		wg.Wait()
	}
	logger.Infof("push and publish space_type: %s, space_id: %s router successfully", params.SpaceType, params.SpaceId)
	return nil
}

// PushSpaceToRedisParams PushSpaceToRedis 任务入参
type PushSpaceToRedisParams struct {
	SpaceType string `json:"space_type"`
	SpaceId   string `json:"space_id"`
}

// PushSpaceToRedis 异步推送创建的空间到 redis
func PushSpaceToRedis(ctx context.Context, t *task.Task) error {
	var params PushSpaceToRedisParams
	if err := jsonx.Unmarshal(t.Payload, &params); err != nil {
		return errors.Wrap(err, fmt.Sprintf("parse params error, %s", err))
	}
	if params.SpaceType == "" || params.SpaceId == "" {
		return errors.New("params space_type or space_id can not be empty")
	}

	logger.Infof("async task start to push space_type: %s, space_id: %s to redis", params.SpaceType, params.SpaceId)

	client, err := dependentredis.GetInstance(ctx)
	if err != nil {
		return errors.Wrapf(err, "get redis client error, %v", err)
	}
	spaceUid := fmt.Sprintf("%s__%s", params.SpaceType, params.SpaceId)
	if err := client.SAdd(cfg.SpaceRedisKey, spaceUid); err != nil {
		return errors.Wrapf(err, "async task push space to redis error, %s", err)
	}
	return nil
}
