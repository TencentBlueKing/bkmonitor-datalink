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
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/jinzhu/gorm"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

//go:generate goqueryset -in event.go -out qs_event.go

// Event: event model
// gen:qs
type Event struct {
	EventID        uint      `json:"event_id" gorm:"event_id;primary_key"`
	EventGroupID   uint      `json:"event_group_id" gorm:"event_group_id"`
	EventName      string    `json:"event_name" gorm:"event_name;size:255"`
	DimensionList  string    `json:"dimension_list" gorm:"dimension_list;"`
	LastModifyTime time.Time `json:"last_modify_time" gorm:"last_modify_time"`
}

func (e *Event) BeforeCreate(tx *gorm.DB) error {
	e.LastModifyTime = time.Now()
	return nil
}

func (e Event) GetDimensionList() []string {
	var dimensionList []string
	sonic.Unmarshal([]byte(e.DimensionList), &dimensionList)
	return dimensionList
}

func (e *Event) SetDimensionList(dimensionList []string) error {
	dimensionBytes, err := sonic.Marshal(dimensionList)
	if err != nil {
		return err
	}
	e.DimensionList = string(dimensionBytes)
	return nil
}

// TableName : 用于设置表的别名
func (Event) TableName() string {
	return "metadata_event"
}

func (e Event) ModifyEventList(eventInfoList map[string][]string) error {
	var evenNameList []string
	for eventName, _ := range eventInfoList {
		evenNameList = append(evenNameList, eventName)
	}
	// 获取已存在的Event
	dbSession := mysql.GetDBSession()
	qs := NewEventQuerySet(dbSession.DB).EventGroupIDEq(e.EventGroupID).EventNameIn(evenNameList...)
	var existEvent []Event
	err := qs.All(&existEvent)
	if err != nil {
		return err
	}
	var eventNameEventMap = new(sync.Map)
	for _, event := range existEvent {
		eventNameEventMap.Store(event.EventName, event)
	}
	// 遍历所有的事件进行处理
	var wg = sync.WaitGroup{}
	wg.Add(len(eventInfoList))
	for eventName, dimensionList := range eventInfoList {
		go func(eventName string, dimensionList []string, eventNameEventMap *sync.Map, wg *sync.WaitGroup) {
			defer wg.Done()

			// 每个Event都需要存在Target
			isTargetDimensionExist := false
			for _, dimension := range dimensionList {
				if dimension == models.EventTargetDimensionName {
					isTargetDimensionExist = true
					break
				}
			}
			if !isTargetDimensionExist {
				dimensionList = append(dimensionList, models.EventTargetDimensionName)
			}
			// 判断Event是否已经存在
			event, ok := eventNameEventMap.Load(eventName)
			// 不存在则新建Event
			if !ok {
				newEvent := Event{
					EventGroupID: e.EventGroupID,
					EventName:    eventName,
				}
				err := newEvent.SetDimensionList(dimensionList)
				if err != nil {
					logger.Errorf("set dimension list [%s] for [%s] [%s] error: %s", dimensionList, e.EventID, newEvent.EventName, err)
					return
				}
				err = newEvent.Create(dbSession.DB)
				if err != nil {
					logger.Errorf("create event [%s] for [%s] error: %s", newEvent.EventName, e.EventID, err)
					return
				}
				eventNameEventMap.Store(newEvent.EventName, newEvent)
				return
			}
			eventObj := event.(Event)
			// 存在则合并dimensions
			newDimensionList := append(eventObj.GetDimensionList(), dimensionList...)
			mergedDimensionList := slicex.StringSet2List(slicex.StringList2Set(newDimensionList))
			eventObj.EventName = eventName
			eventObj.LastModifyTime = time.Now()
			err := eventObj.SetDimensionList(mergedDimensionList)
			if err != nil {
				logger.Errorf(
					"update dimension list [%s] for [%s] [%s] error: %s",
					dimensionList, e.EventID, eventObj.EventName, err,
				)
				return
			}
			err = eventObj.Update(
				dbSession.DB, EventDBSchema.EventName, EventDBSchema.DimensionList, EventDBSchema.LastModifyTime,
			)
			if err != nil {
				logger.Errorf("create event [%s] for [%s] error: %s", eventObj.EventName, e.EventID, err)
				return
			}
		}(eventName, dimensionList, eventNameEventMap, &wg)

	}
	wg.Wait()
	return nil
}
