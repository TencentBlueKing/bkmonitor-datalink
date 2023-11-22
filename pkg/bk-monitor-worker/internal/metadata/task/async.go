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

	"github.com/jinzhu/gorm"
	"github.com/pkg/errors"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
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
	return nil
}

// PublishRedisParams PublishRedis 任务入参
type PublishRedisParams struct {
	SpaceTypeId string `json:"space_type_id"`
	SpaceId     string `json:"space_id"`
	TableId     string `json:"table_id"`
}

// PublishRedis 通知 redis 数据更新
func PublishRedis(ctx context.Context, t *task.Task) error {
	var params PublishRedisParams
	if err := jsonx.Unmarshal(t.Payload, &params); err != nil {
		return errors.Wrap(err, fmt.Sprintf("parse params error, %s", err))
	}

	svc := service.NewSpaceRedisSvc(GetGoroutineLimit("publish_redis"))
	// 如果指定空间，则更新空间信息
	if params.SpaceTypeId != "" && params.SpaceId != "" {
		if err := svc.PushRedisData(params.SpaceTypeId, params.SpaceId, "", params.TableId); err != nil {
			return errors.Wrapf(err, "push space redis data error, %v", err)
		}
		spaceUid := fmt.Sprintf("%s__%s", params.SpaceTypeId, params.SpaceId)
		client, err := dependentredis.GetInstance(ctx)
		if err != nil {
			return errors.Wrapf(err, "get redis client error, %v", err)
		}
		if err := client.Publish(viper.GetString(service.SpaceRedisKeyPath), []string{spaceUid}); err != nil {
			return err
		}
		logger.Infof("%s push and publish %s finished", spaceUid)
		return nil
	}

	if err := svc.PushAndPublishAllSpace("", "", true); err != nil {
		return err
	}
	logger.Infof("push and publish all space finished")
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
	if err := client.SAdd(viper.GetString(service.SpaceRedisKeyPath), spaceUid); err != nil {
		return errors.Wrapf(err, "async task push space to redis error, %s", err)
	}
	return nil
}
