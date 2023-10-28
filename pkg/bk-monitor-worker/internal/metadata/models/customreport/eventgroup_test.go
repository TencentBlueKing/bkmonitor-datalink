// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package customreport

import (
	"context"
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/elasticsearch"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/agiledragon/gomonkey/v2"
	"github.com/docker/docker/pkg/ioutils"
	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/mysql"
	"github.com/stretchr/testify/assert"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestEventGroup_GetESData(t *testing.T) {
	patchNewEsClient := gomonkey.ApplyFunc(EventGroup.GetESClient, func() (*elasticsearch.Elasticsearch, error) {
		return &elasticsearch.Elasticsearch{}, nil
	})

	patchSearchWithBody := gomonkey.ApplyFunc(elasticsearch.Elasticsearch.SearchWithBody, func(es elasticsearch.Elasticsearch, ctx context.Context, index string, body io.Reader) (*elasticsearch.Response, error) {
		all, _ := io.ReadAll(body)
		input := string(all)
		resp := &elasticsearch.Response{StatusCode: 200}
		// mock查询event_name的返回
		if strings.Contains(input, "find_event_name") {
			reader := strings.NewReader(`{"took":80,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":3,"relation":"eq"},"max_score":null,"hits":[]},"aggregations":{"find_event_name":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"event_name_a","doc_count":2},{"key":"event_name_b","doc_count":1}]}}}`)
			resp.Body = ioutils.NewReadCloserWrapper(reader, func() error { return nil })
		} else if strings.Contains(input, `{"query":{"bool":{"must":{"term":{"event_name"`) {
			// mock根据event_name查询dimensions的返回，此处模拟两个event_name
			if strings.Contains(input, "event_name_a") {
				reader := strings.NewReader(`{"took":8,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":2,"relation":"eq"},"max_score":null,"hits":[{"_index":"gse_event_report_base-1","_type":"_doc","_id":"JvRsSooBo_h96XjusmGw","_score":null,"_source":{"event": {"content": "user xxx login failed"},"dimensions": {"module": "db","location": "guangdong","d4": "guangdong"},"target": "127.0.0.1","event_name": "input_your_event_name2","time": "1691392301299"},"sort":[1691392301299000000]}]}}`)
				resp.Body = ioutils.NewReadCloserWrapper(reader, func() error { return nil })
			} else if strings.Contains(input, "event_name_b") {
				reader := strings.NewReader(`{"took":2,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"max_score":null,"hits":[{"_index":"gse_event_report_base-1","_type":"_doc","_id":"EknmSYoBbONGnRlcQn65","_score":null,"_source":{"event": {"content": "user xxx login failed"},"dimensions": {"module2": "db","location": "guangdong"},"target": "127.0.0.1","event_name": "input_your_event_name","time": "1691392301189"},"sort":[1691392301189000000]}]}}`)
				resp.Body = ioutils.NewReadCloserWrapper(reader, func() error { return nil })
			}
		}
		return resp, nil
	})
	defer patchNewEsClient.Reset()
	defer patchSearchWithBody.Reset()
	eg := EventGroup{
		CustomGroupBase: CustomGroupBase{TableID: "gse_event_report_base"},
		EventGroupID:    1,
		EventGroupName:  "eg_name",
	}
	data, err := eg.GetESData(context.TODO())
	assert.Nil(t, err)
	assert.True(t, reflect.DeepEqual(data, map[string][]string{"event_name_a": {"module", "location", "d4"}, "event_name_b": {"module2", "location"}}))
}

func TestEventGroup_ModifyEventList(t *testing.T) {
	patchDBSession := gomonkey.ApplyFunc(mysql.GetDBSession, func() *mysql.DBSession {
		db, err := gorm.Open("mysql", fmt.Sprintf(
			"%s:%s@tcp(%s:%s)/%s?&parseTime=True&loc=Local",
			config.TestStorageMysqlUser,
			config.TestStorageMysqlPassword,
			config.TestStorageMysqlHost,
			config.TestStorageMysqlPort,
			config.TestStorageMysqlDbName,
		))
		assert.Nil(t, err)
		return &mysql.DBSession{DB: db}
	})
	defer patchDBSession.Reset()

	event := Event{
		EventGroupID: 9000,
	}
	dbSession := mysql.GetDBSession()
	var eventList []Event
	// 初始化数据
	err := NewEventQuerySet(dbSession.DB).EventGroupIDEq(event.EventGroupID).All(&eventList)
	assert.Nil(t, err)
	for _, event := range eventList {
		err := event.Delete(dbSession.DB)
		assert.Nil(t, err)
	}
	// 新增一个event:event_name_a
	err = event.ModifyEventList(map[string][]string{"event_name_a": {"module", "location", "d4"}})
	assert.Nil(t, err)
	err = NewEventQuerySet(dbSession.DB).EventGroupIDEq(event.EventGroupID).All(&eventList)
	assert.Nil(t, err)
	assert.Equal(t, len(eventList), 1)
	dimensionList := eventList[0].GetDimensionList()
	targetList := []string{"module", "location", "d4", "target"}
	sort.Strings(dimensionList)
	sort.Strings(targetList)
	assert.True(t, reflect.DeepEqual(dimensionList, targetList))

	// 新增event:event_name_b 并更新event:event_name_a
	err = event.ModifyEventList(map[string][]string{"event_name_a": {"module", "location", "d4", "d5", "d6"}, "event_name_b": {"module2", "location"}})
	assert.Nil(t, err)
	err = NewEventQuerySet(dbSession.DB).EventGroupIDEq(event.EventGroupID).All(&eventList)
	assert.Nil(t, err)
	assert.Equal(t, len(eventList), 2)
	if eventList[0].EventName == "event_name_a" {
		dimensionListA := eventList[0].GetDimensionList()
		targetListA := []string{"module", "location", "d4", "d5", "d6", "target"}
		sort.Strings(dimensionListA)
		sort.Strings(targetListA)
		assert.True(t, reflect.DeepEqual(dimensionListA, targetListA))

		dimensionListB := eventList[1].GetDimensionList()
		targetListB := []string{"module2", "location", "target"}
		sort.Strings(dimensionListB)
		sort.Strings(targetListB)
		assert.True(t, reflect.DeepEqual(dimensionListB, targetListB))
	} else {
		dimensionListA := eventList[1].GetDimensionList()
		targetListA := []string{"module", "location", "d4", "d5", "d6", "target"}
		sort.Strings(dimensionListA)
		sort.Strings(targetListA)
		assert.True(t, reflect.DeepEqual(dimensionListA, targetListA))

		dimensionListB := eventList[0].GetDimensionList()
		targetListB := []string{"module2", "location", "target"}
		sort.Strings(dimensionListB)
		sort.Strings(targetListB)
		assert.True(t, reflect.DeepEqual(dimensionListB, targetListB))
	}
}
