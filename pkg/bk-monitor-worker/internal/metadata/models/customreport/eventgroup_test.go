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
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/docker/docker/pkg/ioutils"
	_ "github.com/jinzhu/gorm/dialects/mysql"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/elasticsearch"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestEventGroup_GetESData(t *testing.T) {
	mocker.InitTestDBConfig("../../../../bmw_test.yaml")
	gomonkey.ApplyMethod(elasticsearch.Elasticsearch{}, "SearchWithBody", func(es elasticsearch.Elasticsearch, ctx context.Context, index string, body io.Reader) (*elasticsearch.Response, error) {
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
	gomonkey.ApplyMethod(elasticsearch.Elasticsearch{}, "Ping", func(es elasticsearch.Elasticsearch) (*elasticsearch.Response, error) {
		return nil, nil
	})
	db := mysql.GetDBSession().DB
	tableId := "gse_event_report_base"

	clusterInfo := storage.ClusterInfo{
		ClusterID:        99,
		ClusterType:      models.StorageTypeES,
		CreateTime:       time.Now(),
		LastModifyTime:   time.Now(),
		RegisteredSystem: "_default",
		Creator:          "system",
		GseStreamToId:    -1,
	}
	db.Delete(&clusterInfo, "cluster_id = ?", clusterInfo.ClusterID)
	err := clusterInfo.Create(db)
	assert.NoError(t, err)
	ess := storage.ESStorage{
		TableID:          tableId,
		StorageClusterID: clusterInfo.ClusterID,
	}
	db.Delete(&ess, "table_id = ?", ess.TableID)
	err = ess.Create(db)
	assert.NoError(t, err)
	eg := EventGroup{
		CustomGroupBase: CustomGroupBase{TableID: tableId},
		EventGroupID:    1,
		EventGroupName:  "eg_name",
	}
	data, err := eg.GetESData(context.TODO())
	assert.Nil(t, err)
	assert.Equal(t, 2, len(data))
	eventNameA, ok := data["event_name_a"]
	assert.True(t, ok)
	targetA := []string{"module", "location", "d4"}
	sort.Strings(eventNameA)
	sort.Strings(targetA)
	assert.Equal(t, targetA, eventNameA)
	eventNameB, ok := data["event_name_b"]
	assert.True(t, ok)
	targetB := []string{"module2", "location"}
	sort.Strings(eventNameB)
	sort.Strings(targetB)
	assert.Equal(t, targetB, eventNameB)
}

func TestEventGroup_UpdateEventDimensionsFromES(t *testing.T) {
	mocker.InitTestDBConfig("../../../../bmw_test.yaml")
	gomonkey.ApplyFuncReturn(EventGroup.GetESData, map[string][]string{}, nil)
	eg := EventGroup{}
	err := eg.UpdateEventDimensionsFromES(context.Background())
	assert.NoError(t, err)
}
