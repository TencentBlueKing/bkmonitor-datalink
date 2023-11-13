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
	"io"
	"strings"
	"sync"

	"github.com/bytedance/sonic"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/elasticsearch"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

//go:generate goqueryset -in eventgroup.go -out qs_eventgroup.go

// EventGroup event group model
// gen:qs
type EventGroup struct {
	CustomGroupBase
	EventGroupID   uint   `json:"event_group_id" gorm:"unique"`
	EventGroupName string `json:"event_group_name" gorm:"size:255"`
}

// TableName 用于设置表的别名
func (eg EventGroup) TableName() string {
	return "metadata_eventgroup"
}

// UpdateEventDimensionsFromES update event dimensions from elasticsearch record
func (eg EventGroup) UpdateEventDimensionsFromES(ctx context.Context) error {
	// 获取 es 中数据，用于后续指标及dimension的更新
	eventInfo, err := eg.GetESData(ctx)
	if err != nil {
		return err
	}
	if len(eventInfo) == 0 {
		return nil
	}
	// 更新 event 操作
	event := Event{EventGroupID: eg.EventGroupID}
	if err := event.ModifyEventList(eventInfo); err != nil {
		return err
	}

	logger.Infof("table: [%s] now process event done", eg.TableID)
	return nil
}

func (eg EventGroup) GetESClient(ctx context.Context) (*elasticsearch.Elasticsearch, error) {
	// 获取对应的es客户端
	dbSession := mysql.GetDBSession()
	qs := storage.NewESStorageQuerySet(dbSession.DB)
	qs = qs.TableIDEq(eg.TableID)
	var esStorage storage.ESStorage
	if err := qs.One(&esStorage); err != nil {
		logger.Errorf("table: [%s] find es storage record error, %v", eg.TableID, err)
		return nil, err
	}
	client, err := esStorage.GetESClient(ctx)
	if err != nil {
		logger.Errorf("get es client error, %v", err)
		return nil, err
	}
	return client, nil
}

func (eg EventGroup) GetESData(ctx context.Context) (map[string][]string, error) {
	client, err := eg.GetESClient(ctx)
	if err != nil {
		return nil, err
	}
	// 获取当前index下，所有的event_name集合
	resp, err := client.SearchWithBody(
		ctx,
		fmt.Sprintf("%s*", eg.TableID),
		strings.NewReader(fmt.Sprintf(
			`{"aggs":{"find_event_name":{"terms":{"field":"event_name","size":%v}}},"size":0}`,
			models.ESQueryMaxSize),
		),
	)
	if err != nil {
		return nil, err
	}
	defer resp.Close()
	if resp.IsError() {
		body, _ := io.ReadAll(resp.Body)
		return nil, errors.Errorf("es resp error, status code [%v], body:[%s]", resp.StatusCode, body)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var eventNameCount EventNameCountResult
	err = sonic.Unmarshal(body, &eventNameCount)
	if err != nil {
		return nil, err
	}

	// 逐个获取Dimension信息
	var eventDimensionData = new(sync.Map)
	var wg = &sync.WaitGroup{}
	wg.Add(len(eventNameCount.Aggregations.FindEventName.Buckets))
	for _, bucket := range eventNameCount.Aggregations.FindEventName.Buckets {

		go func(eventName string, eventDimensionData *sync.Map, wg *sync.WaitGroup) {
			defer wg.Done()
			query := fmt.Sprintf(
				`{"query":{"bool":{"must":{"term":{"event_name":"%s"}}}},"size":1,"sort":{"time":"desc"}}`,
				eventName,
			)

			resp, err := client.SearchWithBody(ctx, fmt.Sprintf("%s*", eg.TableID), strings.NewReader(query))
			if err != nil {
				logger.Errorf("search es index[%s*] body[%s] error, %s", eg.TableID, query, err)
				return
			}
			defer resp.Close()
			if resp.IsError() {
				body, _ := io.ReadAll(resp.Body)
				logger.Errorf("es resp error, status code [%v], body:[%s]", resp.StatusCode, body)
				return
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				logger.Errorf("table_id [%s] read [%s] response body error, %s", eg.TableID, eventName, err)
				return
			}
			var eventDimensionResult EventDimensionResult
			err = sonic.Unmarshal(body, &eventDimensionResult)
			if err != nil {
				logger.Errorf("table_id [%s] unmarshal [%s] response body error, %s", eg.TableID, eventName, err)
				return
			}
			if len(eventDimensionResult.Hits.Hits) == 0 {
				logger.Errorf("table_id [%s] search event_name [%s] return nothing", eg.TableID, eventName)
				return
			}
			// 只需要其中一个命中的结果
			result := eventDimensionResult.Hits.Hits[0]
			var dimensions []string
			for k, _ := range result.Source.Dimensions {
				dimensions = append(dimensions, k)
			}
			eventDimensionData.Store(eventName, dimensions)
			return
		}(bucket.Key, eventDimensionData, wg)

	}
	wg.Wait()
	var eventDimensionList = make(map[string][]string)
	eventDimensionData.Range(func(key, value any) bool {
		eventName, ok := key.(string)
		if !ok {
			return false
		}
		dimensionList, ok := value.([]string)
		if !ok {
			return false
		}
		eventDimensionList[eventName] = dimensionList
		return true
	})
	return eventDimensionList, nil
}

// EventNameCountResult 对event_name字段进行聚合，返回所有事件字段
type EventNameCountResult struct {
	Aggregations struct {
		FindEventName struct {
			Buckets []struct { // event_name列表
				Key      string `json:"key"`
				DocCount int    `json:"doc_count"`
			} `json:"buckets"`
		} `json:"find_event_name"`
	} `json:"aggregations"`
}

// EventDimensionResult 根据event_name查询到的维度列表
type EventDimensionResult struct {
	Hits struct {
		Hits []struct {
			Source struct {
				// 维度信息
				Dimensions map[string]interface{} `json:"dimensions"`
			} `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}
