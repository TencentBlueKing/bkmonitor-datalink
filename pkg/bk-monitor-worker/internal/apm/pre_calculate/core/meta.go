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

	"github.com/google/go-cmp/cmp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
)

// MetadataCenter The configuration center uses DataId as the key and stores basic information,
// including app_name, bk_biz_name, app_id, etc...
type MetadataCenter struct {
	Mapping *sync.Map
	Consul  store.Store
}

// ConsulInfo info of Consul
type ConsulInfo struct {
	Token       string           `json:"token"`
	BkBizId     int              `json:"bk_biz_id"`
	BkTenantId  string           `json:"bk_tenant_id"`
	BkBizName   any              `json:"bk_biz_name"`
	AppId       int              `json:"app_id"`
	AppName     string           `json:"app_name"`
	KafkaInfo   TraceKafkaConfig `json:"kafka_info"`
	TraceEsInfo TraceEsConfig    `json:"trace_es_info"`
	SaveEsInfo  TraceEsConfig    `json:"save_es_info"`
}

// DataIdInfo global DataId info in pre-calculate
type DataIdInfo struct {
	// DataId of trace datasource
	DataId string
	Token  string

	BaseInfo BaseInfo

	TraceEs    TraceEsConfig
	SaveEs     TraceEsConfig
	TraceKafka TraceKafkaConfig
}

// BaseInfo info of bk_biz
type BaseInfo struct {
	BkTenantId string

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

var centerInstance *MetadataCenter

// CreateMetadataCenter globally unique config provider
func CreateMetadataCenter() error {
	consulClient, err := consul.GetInstance()
	if err != nil {
		logger.Errorf("Failed to create Consul client. error: %s", err)
		return err
	}
	centerInstance = &MetadataCenter{
		Mapping: &sync.Map{},
		Consul:  consulClient,
	}
	logger.Infof("Create metadata-center successfully")
	return nil
}

// CreateMockMetadataCenter create fake client
func CreateMockMetadataCenter() error {
	centerInstance = &MetadataCenter{
		Mapping: &sync.Map{},
		Consul:  store.CreateDummyStore(),
	}
	logger.Warnf("Create fake consulClient, make sure you guys are not in production!!!")
	return nil
}

// InitMetadataCenter only for tests
func InitMetadataCenter(c *MetadataCenter) {
	centerInstance = c
}

// AddDataIdAndInfo manually specify the configuration of dataid for testing
func (c *MetadataCenter) AddDataIdAndInfo(dataId, token string, info DataIdInfo) {
	info.DataId = dataId
	info.Token = token
	c.Mapping.Store(dataId, info)
}

// AddDataId get the configuration of this DataId from Consul.
// If this configuration does not exist in Consul, ignored.
func (c *MetadataCenter) AddDataId(dataId string) error {
	info := DataIdInfo{DataId: dataId}
	if err := c.fillInfo(dataId, &info); err != nil {
		return err
	}

	c.Mapping.Store(dataId, info)
	logger.Infof("get DataId info successfully, DataId: %s, info: %+v", dataId, info)
	return nil
}

func (c *MetadataCenter) fillInfo(dataId string, info *DataIdInfo) error {
	key := fmt.Sprintf("%s/apm/data_id/%s", config.StorageConsulPathPrefix, dataId)
	_, bytesData, err := c.Consul.Get(key)
	if err != nil {
		return fmt.Errorf("failed to get key: %s from Consul. error: %s", key, err)
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

	info.Token = apmInfo.Token
	info.BaseInfo = BaseInfo{
		BkTenantId: apmInfo.BkTenantId,

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

// CheckUpdate check the info whether updated
func (c *MetadataCenter) CheckUpdate(dataId string) (bool, string) {
	info := DataIdInfo{DataId: dataId}
	if err := c.fillInfo(dataId, &info); err != nil {
		logger.Warnf("Check DataId updated failed, error: %s", err)
		return false, ""
	}
	v, exist := c.Mapping.Load(dataId)
	if !exist {
		logger.Warnf("Check DataId updated but not found in Mapping!")
		return true, "DataId not found in mapping"
	}

	diff := cmp.Diff(v, info)
	if diff != "" {
		return true, diff
	}
	return false, ""
}

// GetKafkaConfig get kafka config of DataId
func (c *MetadataCenter) GetKafkaConfig(dataId string) TraceKafkaConfig {
	v, _ := c.Mapping.Load(dataId)
	return v.(DataIdInfo).TraceKafka
}

// GetTraceEsConfig get trace es config of DataId
func (c *MetadataCenter) GetTraceEsConfig(dataId string) TraceEsConfig {
	v, _ := c.Mapping.Load(dataId)
	return v.(DataIdInfo).TraceEs
}

// GetSaveEsConfig get save es config of DataId
func (c *MetadataCenter) GetSaveEsConfig(dataId string) TraceEsConfig {
	v, _ := c.Mapping.Load(dataId)
	return v.(DataIdInfo).SaveEs
}

// GetBaseInfo get biz info of DataId
func (c *MetadataCenter) GetBaseInfo(dataId string) BaseInfo {
	v, _ := c.Mapping.Load(dataId)
	return v.(DataIdInfo).BaseInfo
}

// GetToken of DataId
func (c *MetadataCenter) GetToken(dataId string) string {
	v, _ := c.Mapping.Load(dataId)
	return v.(DataIdInfo).Token
}

// GetMetadataCenter return a global metadata provider
func GetMetadataCenter() *MetadataCenter {
	return centerInstance
}
