// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"math/rand"
	"net"
	"os"
	"sync"
	"time"
)

// TimeFieldName : schema time field
var TimeFieldName = "time"

// TimeFieldName : schema time field
var TimeStampFieldName = "timestamp"

// LocalTimeFieldName : record handled local time
var LocalTimeFieldName = "bk_local_time"

// MetricKeyFieldName : exporter metric name
var MetricKeyFieldName = "metric_name"

// MetricValueFieldName : exporter metric value
var MetricValueFieldName = "metric_value"

// RecordMetricsFieldName : record metrics map name
var RecordMetricsFieldName = "metrics"

// RecordDimensionsFieldName : record dimensions map name
var RecordDimensionsFieldName = "dimensions"

// RecordGroupFieldName : record group map name
var RecordGroupFieldName = "group_info"

// RecordBizIDFieldName : record biz id dimension field
var RecordBizIDFieldName = "bk_biz_id"

// RecordBkSetName : record RecordBkSetName dimension field
// var RecordBkSetName = "bk_set_name"

// RecordCloudIDFieldName : record cloud id dimension field
var RecordCloudIDFieldName = "bk_cloud_id"

// RecordCloudIDFieldName : record bk target cloud id dimension field
var RecordTargetCloudIDFieldName = "bk_target_cloud_id"

// RecordSupplierIDFieldName : record supplier id dimension field
var RecordSupplierIDFieldName = "bk_supplier_id"

// RecordIPFieldName : record ip dimension field
var RecordIPFieldName = "ip"

var RecordTmpUserIPFieldName = "_user_tmp_ip_"

// RecordTargetIPFieldName : record target ip dimension field
var RecordTargetIPFieldName = "bk_target_ip"

// RecordTargetHostIDFieldName target hostid
var RecordTargetHostIDFieldName = "bk_target_host_id"

// RecordHostNameFieldName  : record hostname dimension field
var RecordHostNameFieldName = "hostname"

// RecordCMDBLevelFieldName  : CMDB层级信息
var RecordCMDBLevelFieldName = "bk_cmdb_level"

// RecordCMDBLevelIDFieldName  : CMDB层级ID
var RecordCMDBLevelIDFieldName = "bk_inst_id"

// RecordCMDBLevelNameFieldName  : CMDB层级名
var RecordCMDBLevelNameFieldName = "bk_obj_id"

// RecordBkSetID : record RecordBkSetID dimension field
var RecordBkSetID = "bk_set_id"

// RecordBkModuleID : record RecordBkModuleID dimension field
var RecordBkModuleID = "bk_module_id"

// RecordModuleName : level module name
var RecordModuleName = "module"

// RecordBizName : level biz name
var RecordBizName = "biz"

// RecordBizID level biz id
var RecordBizID = "bizid"

// RecordSetName : level set name
var RecordSetName = "set"

// RecordAgentID agent id of bk node
var RecordBKAgentID = "bk_agent_id"

// RecordAgentID cmdb biz id
var RecordBKBizID = "bk_biz_id"

// RecordHostID cmdb host id
var RecordBKHostID = "bk_host_id"

// RecordBkTargetServiceInstanceID : 判断是否为实例上报
var RecordBkTargetServiceInstanceID = "bk_target_service_instance_id"

// RecordEventTargetName: 事件上报目标维度字段名
var RecordEventTargetName = "target"

// RecordEventEventNameName: 事件上报事件名字段
var RecordEventEventNameName = "event_name"

// ProcessID :
var ProcessID string

// ServiceID :
var ServiceID string

// AppName
var AppName string

// BuildHash : git build hash
var BuildHash string

// Version : transfer version
var Version string

// Mode : build mode
var Mode string

var (
	ConfClusterID string
	ConfRootV1    string
)

// PayloadCreatorFunc : factory function to create payload
type PayloadCreatorFunc func() Payload

// DefaultPayloadFormat :
var DefaultPayloadFormat = "json"

// NewDefaultPayload : new default format payload
func NewDefaultPayload() Payload {
	payload, err := NewPayload(DefaultPayloadFormat, rand.Int())
	if err != nil {
		panic(err)
	}
	payload.SetTime(time.Now())
	return payload
}

// Atomic :
type Atomic struct {
	lock sync.RWMutex
}

// View :
func (a *Atomic) View(fn func()) {
	a.lock.RLock()
	defer a.lock.RUnlock()
	fn()
}

// ViewE :
func (a *Atomic) ViewE(fn func() error) error {
	a.lock.RLock()
	defer a.lock.RUnlock()
	return fn()
}

// Update :
func (a *Atomic) Update(fn func()) {
	a.lock.Lock()
	defer a.lock.Unlock()
	fn()
}

// UpdateE :
func (a *Atomic) UpdateE(fn func() error) error {
	a.lock.Lock()
	defer a.lock.Unlock()
	return fn()
}

func initProcessID() {
	interfaces, err := net.Interfaces()
	if err != nil {
		panic(fmt.Errorf("get mac address error: %v", err))
	}
	var addr string
	for _, i := range interfaces {
		if i.Flags&net.FlagUp != 0 && !bytes.Equal(i.HardwareAddr, nil) {
			addr = i.HardwareAddr.String()
			break
		}
	}
	if addr == "" {
		panic(fmt.Errorf("search mac address failed"))
	}

	hash := fnv.New32a()
	_, err = hash.Write([]byte(fmt.Sprintf("%s-%d", addr, os.Getpid())))
	if err != nil {
		panic(fmt.Errorf("calc client id failed: %v", err))
	}
	ProcessID = fmt.Sprintf("transfer-%d", hash.Sum32())
}

func init() {
	initProcessID()
}
