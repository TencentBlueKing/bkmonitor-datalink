// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

//go:generate goqueryset -in accessvmrecord.go -out qs_accessvmrecord_gen.go

// AccessVMRecord access vm record model
// gen:qs
type AccessVMRecord struct {
	DataType         string `json:"data_type" gorm:"size:32"`
	ResultTableId    string `gorm:"result_table_id;size:64" json:"result_table_id"`
	BcsClusterId     string `gorm:"bcs_cluster_id;size:32" json:"bcs_cluster_id"`
	StorageClusterID uint   `gorm:"storage_cluster_id" json:"storage_cluster_id"`
	VmClusterId      uint   `gorm:"vm_cluster_id" json:"vm_cluster_id"`
	BkBaseDataId     uint   `gorm:"bk_base_data_id" json:"bk_base_data_id"`
	VmResultTableId  string `gorm:"vm_result_table_id;size:64" json:"vm_result_table_id"`
	Remark           string `gorm:"size:256" json:"remark"`
}

// TableName 用于设置表的别名
func (AccessVMRecord) TableName() string {
	return "metadata_accessvmrecord"
}

// RefreshVmRouter 更新 vm router
func (a AccessVMRecord) RefreshVmRouter(ctx context.Context) error {
	var db, measurement string
	splits := strings.SplitN(a.ResultTableId, ".", 2)
	if len(splits) != 2 {
		logger.Errorf("table_id: %s not split by '.'", a.ResultTableId)
	} else {
		db = splits[0]
		measurement = splits[1]
	}
	varMap := map[string]interface{}{
		"storageID":   strconv.Itoa(int(a.StorageClusterID)),
		"table_id":    a.ResultTableId,
		"clusterName": "",
		"tagsKey":     []string{},
		"db":          db,
		"vm_rt":       a.VmResultTableId,
		"measurement": measurement,
		"retention_policies": map[string]interface{}{
			"autogen": map[string]interface{}{
				"is_default": true,
				"resolution": 0,
			},
		},
	}
	val, err := jsonx.MarshalString(varMap)
	if err != nil {
		return err
	}
	models.PushToRedis(ctx, models.QueryVmStorageRouterKey, a.ResultTableId, val, false)
	return nil
}

// RefreshVmRouter 更新 vm router
func RefreshVmRouter(ctx context.Context, objs *[]AccessVMRecord, goroutineLimit int) {
	wg := &sync.WaitGroup{}
	ch := make(chan bool, goroutineLimit)
	wg.Add(len(*objs))
	for _, record := range *objs {
		ch <- true
		go func(record AccessVMRecord, wg *sync.WaitGroup, ch chan bool) {
			defer func() {
				<-ch
				wg.Done()
			}()
			err := record.RefreshVmRouter(ctx)
			if err != nil {
				logger.Errorf("vm_result_table: [%s] try to refresh vm router failed, %v", record.VmResultTableId, err)
			} else {
				logger.Infof("vm_result_table: [%s] refresh vm router success", record.VmResultTableId)
			}
		}(record, wg, ch)
	}
	wg.Wait()
	client := redis.GetInstance()
	err := client.Publish(models.InfluxdbKeyPrefix, models.QueryVmStorageRouterKey)
	if err != nil {
		logger.Errorf("publish redis failed, channel: %s, msg: %v, %v", models.InfluxdbKeyPrefix, models.QueryVmStorageRouterKey, err)
	} else {
		logger.Infof("publish redis successfully, channel: %s, msg: %v", models.InfluxdbKeyPrefix, models.QueryVmStorageRouterKey)
	}
}
