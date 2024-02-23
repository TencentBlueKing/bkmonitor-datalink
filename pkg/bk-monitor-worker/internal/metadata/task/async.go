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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
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
		return errors.Wrapf(err, "parse params for CreateEsStorageIndexParams with [%s] error", t.Payload)
	}
	if params.TableId == "" {
		return errors.New("params table_id can not be empty")
	}
	logger.Infof("table_id: %s start to create es index", params.TableId)
	db := mysql.GetDBSession().DB
	var esStorage storage.ESStorage
	if err := storage.NewESStorageQuerySet(db).TableIDEq(params.TableId).One(&esStorage); err != nil {
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
	if err := resulttable.NewResultTableQuerySet(db).TableIdEq(params.TableId).One(&rt); err != nil {
		if gorm.IsRecordNotFoundError(err) {
			logger.Errorf("query ResultTable by table_id [%s] not exist ", params.TableId)
		}
		return err
	}
	if err := service.NewResultTableSvc(&rt).RefreshEtlConfig(); err != nil {
		return errors.Wrapf(err, "refresh etl config for table_id [%s] failed", params.TableId)
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
		return errors.Wrapf(err, "parse params for AccessBkdataVmParams with [%s] error", t.Payload)
	}
	logger.Infof("bk_biz_id [%v] table_id [%s] data_id [%v] start access bkdata vm", params.BkBizId, params.TableId, params.BkDataId)
	if err := service.NewVmUtils().AccessBkdata(params.BkBizId, params.TableId, params.BkDataId); err != nil {
		return errors.Wrapf(err, "bk_biz_id [%v] table_id [%s] data_id [%v] start access bkdata vm failed", params.BkBizId, params.TableId, params.BkDataId)
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
		return errors.Wrapf(err, "parse params for PushAndPublishSpaceRouterParams with [%s] error", t.Payload)
	}
	svc := service.NewSpaceRedisSvc(GetGoroutineLimit("push_and_publish_space_router"))
	return svc.PushAndPublishSpaceRouter(params.SpaceType, params.SpaceId, params.TableIdList)
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
		return errors.Wrapf(err, "parse params for PushSpaceToRedisParams with [%s] error", t.Payload)
	}
	if params.SpaceType == "" || params.SpaceId == "" {
		return errors.New("params space_type or space_id can not be empty")
	}

	logger.Infof("async task start to push space_type: %s, space_id: %s to redis", params.SpaceType, params.SpaceId)

	client := redis.GetInstance()
	spaceUid := fmt.Sprintf("%s__%s", params.SpaceType, params.SpaceId)
	if err := client.SAdd(cfg.SpaceRedisKey, spaceUid); err != nil {
		return errors.Wrap(err, "async task push space to redis error")
	}
	return nil
}

// RefreshCustomReportConfigParams RefreshCustomReportConfig 任务入参
type RefreshCustomReportConfigParams struct {
	BkBizId *int `json:"bk_biz_id"`
}

// RefreshCustomReportConfig refresh custom report to nodeman
func RefreshCustomReportConfig(ctx context.Context, t *task.Task) error {
	var params RefreshCustomReportConfigParams
	if err := jsonx.Unmarshal(t.Payload, &params); err != nil {
		return errors.Wrap(err, "parse params error")
	}
	logger.Infof("async task start to RefreshCustomReport2Config with bk_biz_id [%v]", params.BkBizId)
	if err := service.NewCustomReportSubscriptionSvc(nil).RefreshCustomReport2Config(params.BkBizId); err != nil {
		return errors.Wrap(err, "async RefreshCustomReport2Config failed")
	}
	return nil
}

// RefreshCustomLogReportConfigParams RefreshCustomLogReportConfig 任务入参
type RefreshCustomLogReportConfigParams struct {
	LogGroupId *uint `json:"log_group_id"`
}

// RefreshCustomLogReportConfig Refresh Custom Log Config to Bk Collector
func RefreshCustomLogReportConfig(ctx context.Context, t *task.Task) error {
	var params RefreshCustomLogReportConfigParams
	if err := jsonx.Unmarshal(t.Payload, &params); err != nil {
		return errors.Wrapf(err, "parse params for RefreshCustomLogReportConfigParams with [%s] error", t.Payload)
	}

	logger.Info("async task start to refresh custom log config to collector")

	db := mysql.GetDBSession().DB
	qs := customreport.NewLogGroupQuerySet(db).IsEnableEq(true)
	if params.LogGroupId != nil {
		qs = qs.LogGroupIDEq(*params.LogGroupId)
	}
	var lgs []customreport.LogGroup
	if err := qs.All(&lgs); err != nil {
		return errors.Wrap(err, "query LogGroup failed")
	}

	wg := &sync.WaitGroup{}
	ch := make(chan bool, GetGoroutineLimit("refresh_custom_log_config"))
	wg.Add(len(lgs))
	for _, restore := range lgs {
		ch <- true
		go func(lg customreport.LogGroup, wg *sync.WaitGroup, ch chan bool) {
			defer func() {
				<-ch
				wg.Done()
			}()
			svc := service.NewLogGroupSvc(&lg)
			// Deploy Configs
			if err := svc.Refresh(); err != nil {
				logger.Errorf("RefreshCustomLogConfig [%v(%s)] failed to cron task, %v", svc.LogGroupID, svc.LogGroupName, err)
			} else {
				logger.Infof("RefreshCustomLogConfig [%v(%s)] failed to cron task success", svc.LogGroupID, svc.LogGroupName)
			}
		}(restore, wg, ch)
	}
	wg.Wait()
	return nil
}

// AccessToBkDataParams AccessToBkData 任务入参
type AccessToBkDataParams struct {
	TableId string `json:"table_id"`
}

// AccessToBkData 接入bkdata
func AccessToBkData(ctx context.Context, t *task.Task) error {
	var params AccessToBkDataParams
	if err := jsonx.Unmarshal(t.Payload, &params); err != nil {
		return errors.Wrapf(err, "parse params for AccessToBkDataParams with [%s] error", t.Payload)
	}
	if params.TableId == "" {
		return errors.New("params table_id can not be empty")
	}
	logger.Infof("async task start to access bkdata with table_id [%s]", params.TableId)
	svc := service.NewBkDataStorageSvc(nil)
	if err := svc.CreateTable(params.TableId, true); err != nil {
		return errors.Wrapf(err, "CreateTable for table_id [%s] failed", svc.TableID)
	}
	return nil
}
