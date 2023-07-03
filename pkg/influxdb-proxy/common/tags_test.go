// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package common_test

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
)

var tagNames = []string{"bk_biz_id"}

type TagTestItem struct {
	sql    string
	db     string
	table  string
	result string
}

var tagItems = []TagTestItem{
	{
		"select * from gg1 where bk_biz_id='2' and tag1='3' and tag3='43' and fd<3 or fd>=2 group by time desc",
		"db1",
		"table1",
		"db1/table1/bk_biz_id==2",
	},
	{
		"SELECT MAX(\"usage\") as _value_ FROM cpu_detail WHERE ((bk_target_ip = '10.0.0.1' AND bk_target_cloud_id = '0') AND bk_biz_id = '2') AND time >= 1606115623000000000 AND time < 1606119223000000000 GROUP BY device_name, bk_target_cloud_id, time(1m), bk_target_ip ORDER BY time asc LIMIT 50000",
		"db1",
		"table1",
		"db1/table1/bk_biz_id==2",
	},
	{
		"SELECT last(\"usage\") as usage FROM cpu_summary WHERE bk_biz_id = '2' AND time >= 1606197960000000000  and bk_biz_id='2' GROUP BY ip, bk_cloud_id LIMIT 1",
		"db1",
		"table1",
		"db1/table1/bk_biz_id==2",
	},
}

func TestGetTagsKey(t *testing.T) {
	for index, item := range tagItems {
		tags, err := common.GetSelectTag(tagNames, item.sql)
		if err != nil {
			t.Errorf("get error:%s", err)
			return
		}
		result := common.GetTagsKey(item.db, item.table, tagNames, tags)
		if result != item.result {
			t.Errorf("wrong tag key,index:%d", index)
			return
		}

	}
}

// 测试将一个sql语句解析成tag(用于cluster层路由匹配)需要的损耗
func BenchmarkGetTagsKey(b *testing.B) {
	sql := "SELECT MAX(\"usage\") as _value_ FROM cpu_detail WHERE ((bk_target_ip = '10.0.0.1' AND bk_target_cloud_id = '0') AND bk_biz_id = '2') AND time >= 1606115623000000000 AND time < 1606119223000000000 GROUP BY device_name, bk_target_cloud_id, time(1m), bk_target_ip ORDER BY time asc LIMIT 50000"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tags, err := common.GetSelectTag(tagNames, sql)
		if err != nil {
			b.Error(err)
			return
		}
		common.GetTagsKey("system", "table1", tagNames, tags)
	}
}

type AnaylizeTestItem struct {
	data      string
	db        string
	table     string
	nameList  []string
	valueList []string
}

var anaylizeItems = []AnaylizeTestItem{
	{
		"db1/t1/tag1==3###tag2==22",
		"db1",
		"t1",
		[]string{"tag1", "tag2"},
		[]string{"3", "22"},
	},
}

func TestAnaylizeTags(t *testing.T) {
	for _, item := range anaylizeItems {
		db, table, tags := common.AnaylizeTagsKey(item.data)
		if db != item.db {
			t.Errorf("wrong db")
			return
		}
		if table != item.table {
			t.Errorf("wrong table")
		}
		for index, tag := range tags {
			if string(tag.Key) != item.nameList[index] {
				t.Errorf("wrong tag key,index:%d", index)
				return
			}
			if string(tag.Value) != item.valueList[index] {
				t.Errorf("wrong tag value,index:%d", index)
				return
			}
		}
	}
}
