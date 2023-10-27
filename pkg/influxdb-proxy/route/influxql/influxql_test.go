// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxql

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

type sqlResult struct {
	sql       string
	db        string
	retention string
	table     string
	err       error
}

func TestSQLMatch(t *testing.T) {
	list := []sqlResult{
		{`select * from test`, "", "", "test", nil},
		{`select * from re1.test`, "", "re1", "test", nil},
		{`select * from db1.re1.test`, "db1", "re1", "test", nil},
		{`select * from "tsd_11"`, "", "", "tsd_11", nil},
		{`show databases`, "", "", "", nil},
		{`show measurements`, "", "", "", nil},
		{`show measurements on system`, "system", "", "", nil},
		{`show series`, "", "", "", nil},
		{`show series on db1`, "db1", "", "", nil},
		{`show series from tt2`, "", "", "tt2", nil},
		{`show series on db1 from tt1`, "db1", "", "tt1", nil},
		{`show tag keys`, "", "", "", nil},
		{`show tag keys from t1`, "", "", "t1", nil},
		{`show tag keys on db1`, "db1", "", "", nil},
		{`show tag keys on db1 from t1`, "db1", "", "t1", nil},
		{`show field keys`, "", "", "", nil},
		{`show field keys from t1`, "", "", "t1", nil},
		{`show field keys on db1`, "db1", "", "", nil},
		{`show field keys on db1 from t1`, "db1", "", "t1", nil},
		{`show tag values with key=bb`, "", "", "", nil},
		{`show tag values from t1 with key=bb`, "", "", "t1", nil},
		{`show tag values on db1 with key=bb`, "db1", "", "", nil},
		{`show tag values on db1 from t1 with key=bb`, "db1", "", "t1", nil},
		{`SELECT sum("cnt") FROM "kafka_topic_message_cnt" WHERE ("topic" =~ /^123$/) AND time >= 1573488000000ms GROUP BY time(1m) fill(null)`, "", "", "kafka_topic_message_cnt", nil},
		{`SELECT NON_NEGATIVE_DERIVATIVE(sum("Value")) / 3 as incre FROM (select sum("Value") as Value from "storage_kafka_log" WHERE "name" = 'LogEndOffset' AND "topic" =~ /^undefined$/ AND time >= 1573488000000ms AND Value > 0 group by time(3m)) WHERE time >= 1573488000000ms group by topic, time(3m)`, "", "", "storage_kafka_log", nil},
		{`SELECT mean("value") FROM "storage_kafka_log" WHERE ("name" = 'LogEndOffset') AND time >= 1573488000000ms GROUP BY time(30s) fill(null)`, "", "", "storage_kafka_log", nil},
		{`SELECT min("Value") as "最小Offset", max("Value") as "最大Offset", (max("Value") - min("Value")) as "增量" FROM "storage_kafka_log" WHERE "name" = 'LogEndOffset' AND time >= 1573488000000ms and "topic" =~ /^undefined$/ and "setid" =~ /^$SetID/ AND Value > 0 group by partition, setid`, "", "", "storage_kafka_log", nil},
		{`SELECT sum("cnt") FROM "autogen"."kafka_topic_message_cnt" WHERE ("topic" =~ /^()$/) AND time >= 1573488000000ms GROUP BY time(1m), "partition" fill(null)`, "", "autogen", "kafka_topic_message_cnt", nil},
		{`SELECT sum("cnt") FROM "kafka_topic_message_cnt" WHERE ("topic" =~ /^()$/) AND time >= 1573488000000ms GROUP BY time(1m) fill(null)`, "", "", "kafka_topic_message_cnt", nil},
		{`SELECT "offset" FROM "bkdata_kafka_metrics" WHERE "kafka" = "kafka-inner.service.sz-1.bk:9092" AND "topic" = 302 AND time >= now() - 6h GROUP BY time(1m) fill(null)`, "", "", "bkdata_kafka_metrics", nil},
		{`SELECT sum("data_inc") FROM "data_loss_input_total" WHERE "logical_tag" =~ /^$RT$/ AND "module" =~ /^$module$/ AND "component" =~ /^$component$/ AND time >= 1573488000000ms`, "", "", "data_loss_input_total", nil},
		{`SELECT sum("data_inc") FROM "data_loss_output_total" WHERE time >= 1573488000000ms AND "logical_tag" =~ /^$RT$/ AND "module" =~ /^$module$/ AND "component" =~ /^$component$/`, "", "", "data_loss_output_total", nil},
		{`SELECT sum("data_inc") FROM "rp_InfluxDB"."data_loss_input_total" WHERE ("logical_tag" =~ /^$RT$/ AND "module" =~ /^$module$/ AND "component" =~ /^$component$/) AND time >= 1573488000000ms GROUP BY time(1m) fill(null);`, "", "rp_InfluxDB", "data_loss_input_total", nil},
		{`SELECT sum("data_inc") FROM "rp_InfluxDB"."data_loss_output_total" WHERE ("logical_tag" =~ /^$RT$/ AND "component" =~ /^$component$/ AND "module" =~ /^$module$/) AND time >= 1573488000000ms GROUP BY time(1m) fill(null)`, "", "rp_InfluxDB", "data_loss_output_total", nil},
		{`SELECT max("ab_delay") AS "总延迟", max("fore_delay") AS "前置节点延迟", max("relative_delay") AS "计算延迟", max("window_time") AS "窗口时间", max("waiting_time") AS "等待时间" FROM "rp_InfluxDB"."data_relative_delay" WHERE "logical_tag" =~ /^$RT$/ AND "module" =~ /^$module$/ AND "component" =~ /^$component$/ AND time >= 1573488000000ms GROUP BY time(1m) fill(null)`, "", "rp_InfluxDB", "data_relative_delay", nil},
		{`SELECT max("relative_delay") AS "计算延迟" FROM "rp_InfluxDB"."data_relative_delay" WHERE ("logical_tag" =~ /^$RT$/ AND "component" =~ /^$component$/ AND "module" =~ /^$module$/) AND time >= 1573488000000ms GROUP BY time(5m), "physical_tag" fill(0)`, "", "rp_InfluxDB", "data_relative_delay", nil},
		{`SELECT sum("loss_cnt") FROM "data_loss_audit" WHERE "logical_tag" =~ /^$RT$/ AND time >= 1573488000000ms AND "module" =~ /^$module$/ AND "component" =~ /^$component$/`, "", "", "data_loss_audit", nil},
		{`SELECT sum("loss_cnt") AS "丢失量", sum("output_cnt") AS "输出量", sum("consume_cnt") AS "输出被消费" FROM "rp_InfluxDB"."data_loss_audit" WHERE ("logical_tag" =~ /^$RT$/ AND "module" =~ /^$module$/ AND "component" =~ /^$component$/) AND time >= 1573488000000ms GROUP BY time(2m) fill(null)`, "", "rp_InfluxDB", "data_loss_audit", nil},
		{`SELECT median("drop_rate") FROM "data_loss_drop_rate" WHERE "logical_tag" =~ /^$RT$/ AND time >= 1573488000000ms AND "module" =~ /^$module$/ AND "component" =~ /^$component$/`, "", "", "data_loss_drop_rate", nil},
		{`SELECT sum("data_cnt") FROM "data_loss_drop" WHERE "logical_tag" =~ /^$RT$/ AND time >= 1573488000000ms AND "module" =~ /^$module$/ AND "component" =~ /^$component$/`, "", "", "data_loss_drop", nil},
		{`SELECT max("drop_cnt") AS "丢弃条数", max("drop_rate") AS "丢弃率", max("message_cnt") AS "消息条数" FROM "rp_InfluxDB"."data_loss_drop_rate" WHERE ("logical_tag" =~ /^$RT$/ AND "module" =~ /^$module$/ AND "component" =~ /^$component$/) AND time >= 1573488000000ms GROUP BY time(2m) fill(null)`, "", "rp_InfluxDB", "data_loss_drop_rate", nil},
		{`SELECT sum("output_cnt") AS "输出条数", sum("consume_cnt") AS "消费条数", sum("loss_cnt") AS "丢失条数" FROM "data_loss_audit" WHERE "loss_cnt" > 0 AND "logical_tag" =~ /^$RT$/ AND time >= 1573488000000ms AND "module" =~ /^$module$/ AND "component" =~ /^$component$/ GROUP BY "dst_logical_tag"`, "", "", "data_loss_audit", nil},
		{`SELECT "data_cnt" as "丢弃条数", "reason" as "丢弃原因", "physical_tag" as "物理标识" FROM "rp_InfluxDB"."data_loss_drop" WHERE time >= 1573488000000ms and logical_tag =~ /^$RT$/ AND "module" =~ /^$module$/ AND "component" =~ /^$component$/ order by time desc`, "", "rp_InfluxDB", "data_loss_drop", nil},
		{`SELECT sum("data_cnt") AS "输出条数" FROM "data_loss_output" WHERE ("logical_tag" =~ /^$RT$/ AND "component" =~ /^$component$/ AND "module" =~ /^$module$/) AND time >= 1573488000000ms GROUP BY time(1m) fill(null)`, "", "", "data_loss_output", nil},
		{`SELECT "data_cnt" AS "输出条数", "output_tag" AS "数据Tag", "physical_tag" as "物理标识" FROM "data_loss_output" WHERE "logical_tag" =~ /^$RT$/ and time >= 1573488000000ms AND "module" =~ /^$module$/ AND "component" =~ /^$component$/ order by time desc`, "", "", "data_loss_output", nil},
		{`SELECT "dst_logical_tag","output_cnt", "consume_cnt", "loss_cnt", "fail_detail" FROM "data_loss_audit" WHERE "module" =~ /^$module$/ AND "component" =~ /^$component$/ AND "logical_tag" =~ /^$RT$/ AND time >= 1573488000000ms AND loss_cnt != 0 order by time desc`, "", "", "data_loss_audit", nil},
	}

	for idx, item := range list {
		dataSource, err := GetSingleDataSource(item.sql)
		if assert.Nil(t, err, fmt.Sprintf("line::%d", idx)) {
			assert.Equal(t, item.db, dataSource.GetDB())
			assert.Equal(t, item.retention, dataSource.GetRetention())
			assert.Equal(t, item.table, dataSource.GetTable())
		}
	}
}
