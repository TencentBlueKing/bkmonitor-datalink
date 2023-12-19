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
	"reflect"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestDimensions(t *testing.T) {
	var dimensionObj = []string{"d1", "d2", "d3", "d4"}
	event := &Event{
		EventID:       123,
		EventGroupID:  1,
		EventName:     "test_event",
		DimensionList: `["d1","d2","d3","d4"]`,
	}

	assert.True(t, reflect.DeepEqual(dimensionObj, event.GetDimensionList()))

	dimensionObj = dimensionObj[:1]
	err := event.SetDimensionList(dimensionObj)
	assert.Nil(t, err)
	assert.True(t, reflect.DeepEqual(dimensionObj, event.GetDimensionList()))
}

func TestEvent_ModifyEventList(t *testing.T) {
	mocker.InitTestDBConfig("../../../../bmw_test.yaml")
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
