// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package core

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
)

// MetadataCenter The configuration center uses dataId as the key and stores basic information,
// including app_name, bk_biz_name, app_id, etc...
type MetadataCenter struct {
	mapping *sync.Map
	consul  consul.Instance
}

// ConsulInfo info of consul
type ConsulInfo struct {
	BkBizId     int              `json:"bk_biz_id"`
	BkBizName   any              `json:"bk_biz_name"`
	AppId       int              `json:"app_id"`
	AppName     string           `json:"app_name"`
	KafkaInfo   TraceKafkaConfig `json:"kafka_info"`
	TraceEsInfo TraceEsConfig    `json:"trace_es_info"`
	SaveEsInfo  TraceEsConfig    `json:"save_es_info"`
}

// DataIdInfo global dataId info in pre-calculate
type DataIdInfo struct {
	dataId string

	BaseInfo BaseInfo

	TraceEs    TraceEsConfig
	SaveEs     TraceEsConfig
	TraceKafka TraceKafkaConfig
}

// BaseInfo info of bk_biz
type BaseInfo struct {
	BkBizId   string
	BkBizName string
	AppId     string
	AppName   string
}

// TraceEsConfig es config
type TraceEsConfig struct {
	IndexName string `json:"index_name"`
	Host      string `json:"host"`
	Username  string `json:"username"`
	Password  string `json:"password"`
}

// TraceKafkaConfig kafka configuration for span
type TraceKafkaConfig struct {
	Topic    string `json:"topic"`
	Host     string `json:"host"`
	Username string `json:"username"`
	Password string `json:"password"`
}

var (
	centerInstance *MetadataCenter
)

// CreateMetadataCenter globally unique config provider
func CreateMetadataCenter() error {
	consulClient, err := consul.GetInstance()
	if err != nil {
		logger.Errorf("Failed to create consul client. error: %s", err)
		return err
	}
	centerInstance = &MetadataCenter{
		mapping: &sync.Map{},
		consul:  *consulClient,
	}
	logger.Infof("Create metadata-center successfully")
	return nil
}

// AddDataIdAndInfo manually specify the configuration of dataid for testing
func (c *MetadataCenter) AddDataIdAndInfo(dataId string, info DataIdInfo) {
	info.dataId = dataId
	c.mapping.Store(dataId, info)
}

// AddDataId get the configuration of this dataId from consul.
// If this configuration does not exist in consul, ignored.
func (c *MetadataCenter) AddDataId(dataId string) error {
	info := DataIdInfo{dataId: dataId}
	if err := c.fillInfo(dataId, &info); err != nil {
		return err
	}

	c.mapping.Store(dataId, info)
	logger.Infof("get dataId info successfully, dataId: %s, info: %+v", dataId, info)
	return nil
}

func (c *MetadataCenter) fillInfo(dataId string, info *DataIdInfo) error {
	key := fmt.Sprintf("%s/apm/data_id/%s", config.StorageConsulPathPrefix, dataId)
	bytesData, err := c.consul.Get(key)
	if err != nil {
		return fmt.Errorf("failed to get key: %s from consul. error: %s", key, err)
	}
	if bytesData == nil {
		return fmt.Errorf("failed to get value as key: %s maybe not exist", key)
	}

	var apmInfo ConsulInfo
	if err = jsonx.Unmarshal(bytesData, &apmInfo); err != nil {
		return fmt.Errorf("failed to parse value to ApmInfo, value: %s. error: %s", bytesData, err)
	}

	// if it is a business of space-type, then the bkBizName is negative(eg. -4332771)
	var bizName string
	switch apmInfo.BkBizName.(type) {
	case float64:
		bizName = strconv.FormatFloat(apmInfo.BkBizName.(float64), 'f', -1, 64)
	default:
		bizName = apmInfo.BkBizName.(string)
	}

	info.BaseInfo = BaseInfo{
		BkBizId:   strconv.Itoa(apmInfo.BkBizId),
		BkBizName: bizName,
		AppId:     strconv.Itoa(apmInfo.AppId),
		AppName:   apmInfo.AppName,
	}
	info.TraceKafka = apmInfo.KafkaInfo
	info.TraceEs = apmInfo.TraceEsInfo
	info.SaveEs = apmInfo.SaveEsInfo
	return nil
}

// GetKafkaConfig get kafka config of dataId
func (c *MetadataCenter) GetKafkaConfig(dataId string) TraceKafkaConfig {
	v, _ := c.mapping.Load(dataId)
	return v.(DataIdInfo).TraceKafka
}

// GetTraceEsConfig get trace es config of dataId
func (c *MetadataCenter) GetTraceEsConfig(dataId string) TraceEsConfig {
	v, _ := c.mapping.Load(dataId)
	return v.(DataIdInfo).TraceEs
}

// GetSaveEsConfig get save es config of dataId
func (c *MetadataCenter) GetSaveEsConfig(dataId string) TraceEsConfig {
	v, _ := c.mapping.Load(dataId)
	return v.(DataIdInfo).SaveEs
}

// GetBaseInfo get biz info of dataId
func (c *MetadataCenter) GetBaseInfo(dataId string) BaseInfo {
	v, _ := c.mapping.Load(dataId)
	return v.(DataIdInfo).BaseInfo
}

// GetMetadataCenter return a global metadata provider
func GetMetadataCenter() *MetadataCenter {
	return centerInstance
}
