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
	"strings"

	"github.com/jinzhu/gorm"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bkdata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/stringx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// BkDataStorageSvc bkdata storage service
type BkDataStorageSvc struct {
	*storage.BkDataStorage
}

func NewBkDataStorageSvc(obj *storage.BkDataStorage) BkDataStorageSvc {
	return BkDataStorageSvc{
		BkDataStorage: obj,
	}
}

func (s BkDataStorageSvc) CreateDatabusClean(rt *resulttable.ResultTable) error {
	if s.BkDataStorage == nil {
		return errors.New("BkDataStorage obj can not be nil")
	}
	db := mysql.GetDBSession().DB
	var kafkaStorage storage.KafkaStorage
	if err := storage.NewKafkaStorageQuerySet(db.New()).TableIDEq(rt.TableId).One(&kafkaStorage); err != nil {
		if gorm.IsRecordNotFoundError(err) {
			logger.Errorf("result table [%s] data not write into mq", rt.TableId)
		}
		return err
	}
	// 增加接入部署计划
	svc := NewKafkaStorageSvc(&kafkaStorage)
	storageCluster, err := svc.StorageCluster()
	if err != nil {
		return err
	}
	consulConfig := NewClusterInfoSvc(storageCluster).ConsulConfig()
	domain := consulConfig.ClusterConfig.DomainName
	port := consulConfig.ClusterConfig.Port
	// kafka broker_url 以实际配置为准，如果没有配置，再使用默认的 broker url
	brokerUrl := config.GlobalBkdataKafkaBrokerUrl
	if domain != "" && port != 0 {
		brokerUrl = fmt.Sprintf("%s:%v", domain, port)
	}
	isSasl := consulConfig.ClusterConfig.IsSslVerify
	user := consulConfig.AuthInfo.Username
	passwd := consulConfig.AuthInfo.Password
	// 采用结果表区分消费组
	KafkaConsumerGroupName := GenBkdataRtIdWithoutBizId(rt.TableId)
	// 计算平台要求，raw_data_name不能超过50个字符
	rtId := strings.ReplaceAll(rt.TableId, ".", "__")
	rtId = stringx.LimitLengthSuffix(rtId, 50)
	rawDataName := fmt.Sprintf("%s_%s", config.GlobalBkdataRtIdPrefix, rtId)

	params := map[string]interface{}{
		"bk_app_code":   config.BkApiAppCode,
		"bk_username":   "admin",
		"data_scenario": "queue",
		"bk_biz_id":     config.GlobalBkdataBkBizId,
		"description":   "",
		"access_raw_data": map[string]interface{}{
			"raw_data_name":    rawDataName,
			"maintainer":       config.GlobalBkdataProjectMaintainer,
			"raw_data_alias":   rt.TableNameZh,
			"data_source":      "kafka",
			"data_encoding":    "UTF-8",
			"sensitivity":      "private",
			"description":      fmt.Sprintf("接入配置 (%s)", rt.TableNameZh),
			"tags":             []interface{}{},
			"data_source_tags": []string{"src_kafka"},
		},
		"access_conf_info": map[string]interface{}{
			"collection_model": map[string]interface{}{"collection_type": "incr", "start_at": 1, "period": "-1"},
			"resource": map[string]interface{}{
				"type": "kafka",
				"scope": []map[string]interface{}{
					{
						"master":            brokerUrl,
						"group":             KafkaConsumerGroupName,
						"topic":             svc.Topic,
						"tasks":             svc.Partition,
						"use_sasl":          isSasl,
						"security_protocol": "SASL_PLAINTEXT",
						"sasl_mechanism":    "SCRAM-SHA-512",
						"user":              user,
						"password":          passwd,
					},
				},
			},
		},
	}
	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return err
	}
	var resp bkdata.AccessDeployPlanResp
	if _, err := bkdataApi.AccessDeployPlan().SetBody(params).SetResult(&resp).Request(); err != nil {
		return errors.Wrapf(err, "access to bkdata failed, params [%#v]", params)
	}
	s.RawDataID = resp.Data.RawDataId
	if s.RawDataID == 0 {
		return fmt.Errorf("access to bkdata failed, %s", resp.Message)
	}
	logger.Infof("access to bkdata, result [%#v]", resp)

	if err := s.Update(db, storage.BkDataStorageDBSchema.RawDataID); err != nil {
		return err
	}
	return nil
}

func GenBkdataRtIdWithoutBizId(tableId string) string {
	tableIdConv := strings.ReplaceAll(tableId, ".", "_")
	tableIdConv = stringx.LimitLengthSuffix(tableIdConv, 32)
	rtId := strings.ToLower(fmt.Sprintf("%s_%s", config.GlobalBkdataRtIdPrefix, tableIdConv))
	return strings.TrimLeft(rtId, "_")
}
