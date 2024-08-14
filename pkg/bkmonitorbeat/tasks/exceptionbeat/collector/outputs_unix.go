// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos
// +build aix darwin dragonfly freebsd linux netbsd openbsd solaris zos

package collector

import (
	"strconv"
	"sync/atomic"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/common"
	gselib "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/gse"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/output/gse"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var rawID int64

type ExceptionBeatEvent struct {
	m common.MapStr
}

var (
	NodeIP  string
	BizID   int32
	CloudID int32
)

func Init() {
	const (
		outputConfigName = "output"
		gseConfigName    = "bkpipe"
	)

	rawconfig := beat.GetRawConfig()
	if rawconfig == nil {
		return
	}
	outputrawconfig, err := rawconfig.Child(outputConfigName, -1)
	if err != nil {
		return
	}
	ok := outputrawconfig.HasField(gseConfigName)
	if !ok {
		return
	}

	gserawconfig, err := outputrawconfig.Child(gseConfigName, -1)
	client, err := gselib.NewGseClient(gserawconfig)
	if err != nil {
		logger.Errorf("new gse client failed: %v", err)
		return
	}

	if err = client.Start(); err != nil {
		logger.Errorf("start gse client failed: %v", err)
		return
	}
	defer client.CloseSilent()

	var config gse.Config
	err = gserawconfig.Unpack(&config)
	if err != nil {
		logger.Errorf("unpack gse config failed: %v", err)
		return
	}

	// 这里需要 sleep 一下 不然获取的 AgentInfo 的信息
	time.Sleep(time.Second * 3)
	fetcher := gse.NewAgentInfoFetcher(config, client)
	info, err := fetcher.Fetch()
	if err != nil {
		logger.Errorf("fetch agent info failed: %v", err)
		return
	}

	NodeIP = info.IP
	BizID = info.Bizid
	CloudID = info.Cloudid
}

func (e ExceptionBeatEvent) AsMapStr() common.MapStr {
	return e.m
}

func (e ExceptionBeatEvent) IgnoreCMDBLevel() bool { return true }

func (e ExceptionBeatEvent) GetType() string {
	return define.ModuleExceptionbeat
}

var Send = func(dataid int, extra beat.MapStr, e chan<- define.Event) {
	var bulk []beat.MapStr
	bulk = append(bulk, extra)
	SendBulk(dataid, bulk, e)
}

func SendBulk(dataid int, extra []beat.MapStr, e chan<- define.Event) {
	newID := atomic.AddInt64(&rawID, 1)
	nowtime := time.Now()
	_, zone := nowtime.Zone()
	nowtimestr := nowtime.Format("2006-01-02 15:04:05")
	utcstr := nowtime.UTC().Format("2006-01-02 15:04:05")
	for _, extraitem := range extra {
		eventTimeStr := nowtimestr
		if vStr, ok := extraitem["event_time"]; ok {
			if text, ok := vStr.(string); ok && text != "" {
				timeStamp, err := strconv.ParseInt(text, 10, 64)
				if err == nil {
					eventTime := time.Unix(timeStamp, 0)
					eventTimeStr = eventTime.Format("2006-01-02 15:04:05")
					utcstr = eventTime.UTC().Format("2006-01-02 15:04:05")
				}
			}
		}

		delete(extraitem, "event_time")
		value := []beat.MapStr{
			{
				"event_desc":          "",
				"event_raw_id":        newID - 1,
				"event_source_system": "",
				"event_time":          eventTimeStr,
				"event_title":         "",
				"event_type":          "gse_basic_alarm_type",
				"extra":               extraitem,
			},
		}

		event := beat.MapStr{
			"dataid":   dataid,
			"isdst":    0,
			"server":   NodeIP,
			"time":     nowtimestr,
			"timezone": zone / 3600,
			"utctime":  eventTimeStr,
			"utctime2": utcstr,
			"value":    value,
		}
		logger.Infof("Message content that will be sent: %v", event)

		e <- ExceptionBeatEvent{m: event}
	}
}
