// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package poller

import (
	"encoding/json"
	"time"

	consulapi "github.com/hashicorp/consul/api"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/logging"
)

// 最大拉取时间范围
const MaxIntervalMinutes = 30

type TimeMaker struct {
	DataID    int
	Interval  int
	Format    string
	Overlap   int
	beginTime time.Time
	endTime   time.Time
}

type TimeMakerContext struct {
	LastCheckTime int64 `json:"last_check_time"`
}

func (t *TimeMaker) getBeginTime() time.Time {
	logger := logging.GetLogger()

	beginTime := t.endTime.Add(time.Duration(-(t.Interval + t.Overlap)) * time.Second)

	// 从Consul获取当前 dataid 的最后一次拉取时间
	client, err := consul.NewClient()
	if err != nil {
		logger.Debugf("consul client init faield: %+v", err)
		return beginTime
	}
	contextPath := config.Configuration.Consul.GetDataIDContextPath(t.DataID)
	kvPair, _, err := client.KV().Get(contextPath, nil)
	if err != nil {
		logger.Debugf("consul key->(%s) get faield: %+v", contextPath, err)
		return beginTime
	}

	if kvPair == nil {
		logger.Debugf("consul key->(%s) is empty, will use default beginTime", contextPath)
		return beginTime
	}

	timeMakerContext := &TimeMakerContext{}
	err = json.Unmarshal(kvPair.Value, timeMakerContext)
	if err != nil {
		logger.Debugf("consul key->(%s) unmarshal faield: %+v", contextPath, err)
		return beginTime
	}

	// 如果记录的时间超出了最大范围，则直接取最大范围，避免拉的时间跨度太大
	defaultBeginTime := t.endTime.Add(time.Duration(-MaxIntervalMinutes) * time.Minute)
	lastCheckTime := time.Unix(timeMakerContext.LastCheckTime, 0)
	if lastCheckTime.Before(defaultBeginTime) {
		beginTime = defaultBeginTime
	} else if lastCheckTime.Before(beginTime) {
		// 如果最新检测时间大于1个周期，则用lastCheckTime，否则最少拉取一个周期
		beginTime = lastCheckTime
	}
	return beginTime
}

func (t *TimeMaker) GetTimeRange() Context {
	timeLayout, ok := define.GetTimeLayout(t.Format)
	if !ok {
		timeLayout = "epoch_second"
	}

	nowTime := time.Now()

	t.endTime = nowTime.Truncate(time.Duration(t.Interval) * time.Second)
	t.beginTime = t.getBeginTime()

	return Context{
		"begin_time": define.FormatTimeByLayout(timeLayout, t.beginTime),
		"end_time":   define.FormatTimeByLayout(timeLayout, t.endTime),
	}
}

func (t *TimeMaker) CommitLastCheckTime() {
	// 将当前 dataid 的最后一次拉取时间写入到 consul

	logger := logging.GetLogger()

	client, err := consul.NewClient()
	if err != nil {
		logger.Errorf("consul client init faield: %+v", err)
		return
	}

	checkTime := t.endTime.Unix()

	contextData, err := json.Marshal(&TimeMakerContext{
		LastCheckTime: checkTime,
	})
	if err != nil {
		logger.Errorf("TimeMakerContext marshal faield: %+v", err)
		return
	}

	contextPath := config.Configuration.Consul.GetDataIDContextPath(t.DataID)
	_, err = client.KV().Put(&consulapi.KVPair{
		Key:   contextPath,
		Value: contextData,
	}, nil)
	if err != nil {
		logger.Errorf("consul key->(%s) put faield: %+v", contextPath, err)
	}

	logger.Debugf("data_id->(%d) last_check_time is updated: %d", t.DataID, checkTime)
}
