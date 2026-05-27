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
	IsShared    bool             `json:"is_shared"`
	BkBizId     int              `json:"bk_biz_id"`
	BkTenantId  string           `json:"bk_tenant_id"`
	BkBizName   any              `json:"bk_biz_name"`
	AppId       int              `json:"app_id"`
	AppName     string           `json:"app_name"`
	Apps        []ConsulAppInfo  `json:"apps"`
	KafkaInfo   TraceKafkaConfig `json:"kafka_info"`
	TraceEsInfo TraceEsConfig    `json:"trace_es_info"`
	SaveEsInfo  TraceEsConfig    `json:"save_es_info"`
}

// ConsulAppInfo describes one app in a shared data_id consul payload.
type ConsulAppInfo struct {
	Token      string `json:"token"`
	BkBizId    int    `json:"bk_biz_id"`
	BkTenantId string `json:"bk_tenant_id"`
	BkBizName  any    `json:"bk_biz_name"`
	AppId      int    `json:"app_id"`
	AppName    string `json:"app_name"`
}

// DataIdInfo global DataId info in pre-calculate
type DataIdInfo struct {
	// DataId of trace datasource
	DataId   string
	Token    string
	IsShared bool

	BaseInfo BaseInfo
	Apps     map[AppKey]BaseInfo

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
	Token     string
}

// AppKey identifies one APM application under a trace data_id.
type AppKey struct {
	BkBizId string
	AppName string
}

func (b BaseInfo) AppKey() AppKey {
	return AppKey{BkBizId: b.BkBizId, AppName: b.AppName}
}

func (k AppKey) IsZero() bool {
	return k.BkBizId == "" || k.AppName == ""
}

func newBaseInfo(token, bkTenantId string, bkBizId int, bkBizName any, appId int, appName string) BaseInfo {
	return BaseInfo{
		Token:      token,
		BkTenantId: bkTenantId,
		BkBizId:    strconv.Itoa(bkBizId),
		BkBizName:  formatBizName(bkBizName),
		AppId:      strconv.Itoa(appId),
		AppName:    appName,
	}
}

func addBaseInfo(apps map[AppKey]BaseInfo, baseInfo BaseInfo) {
	if appKey := baseInfo.AppKey(); !appKey.IsZero() {
		apps[appKey] = baseInfo
	}
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
	if info.IsShared {
		if info.Apps == nil {
			info.Apps = make(map[AppKey]BaseInfo)
		}
	} else {
		if info.BaseInfo.Token == "" {
			info.BaseInfo.Token = token
		}
		info.Apps = make(map[AppKey]BaseInfo, 1)
		addBaseInfo(info.Apps, info.BaseInfo)
	}
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

	info.Token = apmInfo.Token
	info.IsShared = apmInfo.IsShared
	if info.IsShared {
		info.Apps = make(map[AppKey]BaseInfo, len(apmInfo.Apps))
		for _, app := range apmInfo.Apps {
			addBaseInfo(info.Apps, newBaseInfo(
				app.Token,
				app.BkTenantId,
				app.BkBizId,
				app.BkBizName,
				app.AppId,
				app.AppName,
			))
		}
	} else {
		info.Apps = make(map[AppKey]BaseInfo, 1)
		info.BaseInfo = newBaseInfo(
			apmInfo.Token,
			apmInfo.BkTenantId,
			apmInfo.BkBizId,
			apmInfo.BkBizName,
			apmInfo.AppId,
			apmInfo.AppName,
		)
		addBaseInfo(info.Apps, info.BaseInfo)
	}
	info.TraceKafka = apmInfo.KafkaInfo
	info.TraceEs = apmInfo.TraceEsInfo
	info.SaveEs = apmInfo.SaveEsInfo
	return nil
}

func formatBizName(v any) string {
	// if it is a business of space-type, then the bkBizName is negative(eg. -4332771)
	switch value := v.(type) {
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	default:
		return value.(string)
	}
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

func (c *MetadataCenter) IsShared(dataId string) bool {
	v, _ := c.Mapping.Load(dataId)
	return v.(DataIdInfo).IsShared
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

// ListBaseInfos lists app contexts under a DataId.
func (c *MetadataCenter) ListBaseInfos(dataId string) []BaseInfo {
	v, _ := c.Mapping.Load(dataId)
	info := v.(DataIdInfo)
	baseInfos := make([]BaseInfo, 0, len(info.Apps))
	for _, baseInfo := range info.Apps {
		baseInfos = append(baseInfos, baseInfo)
	}
	return baseInfos
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
