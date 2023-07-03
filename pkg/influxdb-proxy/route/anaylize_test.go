// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package route_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/route"
)

func getStrList(num int) string {
	strList := ""
	bizCount := 5
	for i := 0; i < num; i++ {
		strList = strList + fmt.Sprintf("mymeas,bk_biz_id=%d,mytag=%d myfield=90 1463683075000000000\n", i%bizCount, i)
	}
	return strList
}

// 测试将数据解析成point的效果
func TestAnaylizeTagData(t *testing.T) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  "test",
		"flow_id": "12345x",
	})
	strList := getStrList(400)
	ch := make(chan common.Points)
	go func() {
		for points := range ch {
			if len(points) != 200 && len(points) != 0 {
				t.Errorf("wrong number of data:%d", len(points))
			}
		}
	}()
	_ = route.AnaylizeTagData(0, ch, "system", 200, []byte(strList), flowLog)
	time.Sleep(3 * time.Second)
}

func TestAnaylizeRealTagData(t *testing.T) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  "test",
		"flow_id": "12345x",
	})
	strList := "base,bk_biz_id=9,bk_cloud_id=0,bk_collect_config_id=171,bk_supplier_id=0,bk_target_cloud_id=0,bk_target_ip=10.0.0.1,bk_target_service_instance_id=5237,bk_target_topo_id=1077,bk_target_topo_level=module,dc_name=Datacenter_shenzheng,host_name=10.0.0.1,ip=10.0.0.1,metric_name=vmware_vm_guest_disk_capacity,partition=E:\\\\\\\\e,vm_name=nzmobile_ruiqingkong_10.0.0.1 metric_value=1541891158016 1611215601000000000"
	ch := make(chan common.Points)
	go func() {
		for points := range ch {
			if len(points) != 1 && len(points) != 0 {
				t.Errorf("wrong number of data:%d", len(points))
			}
		}
	}()
	_ = route.AnaylizeTagData(0, ch, "system", 200, []byte(strList), flowLog)
	time.Sleep(3 * time.Second)
}

// 测试将解析数据的耗时程度
func BenchmarkAnaylizeTagData(b *testing.B) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  "test",
		"flow_id": "12345x",
	})
	strList := getStrList(15000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ch := make(chan common.Points, 10000)
		_ = route.AnaylizeTagData(0, ch, "system", 200, []byte(strList), flowLog)
	}
}
