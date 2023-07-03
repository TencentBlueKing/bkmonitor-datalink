// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

import (
	"context"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
)

// GetPathByKey :
func GetPathByKey(conf define.Configuration, path ...string) string {
	parts := make([]string, 0, len(path))
	for _, p := range path {
		parts = append(parts, conf.GetString(p))
	}
	return strings.Join(parts, "/")
}

// SourceDriver :
type SourceDriver struct {
	ctx           context.Context
	conf          define.Configuration
	SrcCli        SourceClient
	dataIDSubPath string
}

// NewSourceDriver :
func NewSourceDriver(ctx context.Context, sc SourceClient) *SourceDriver {
	conf := config.FromContext(ctx)
	return &SourceDriver{
		SrcCli:        sc,
		ctx:           ctx,
		dataIDSubPath: GetPathByKey(conf, ConfKeyDataIDPath),
		conf:          conf,
	}
}

// MonitorDataID :
func (sd *SourceDriver) MonitorDataID() (<-chan *CfgEvent, error) {
	dataIDPath := GetPathByKey(sd.conf, ConfKeyDataIDPath)
	ch, err := sd.SrcCli.MonitorPath([]string{dataIDPath})
	if err != nil {
		logging.Warnf("monitor root %s error: %v", dataIDPath, err)
		return nil, err
	}
	logging.Infof("monitoring root %s", dataIDPath)
	eventBufferSize := 0
	if sd.conf != nil {
		eventBufferSize = sd.conf.GetInt(ConfKeyEventBufferSize)
	}
	cfgCh := make(chan *CfgEvent, eventBufferSize)

	go func() {
	loop:
		for {
			select {
			case <-sd.ctx.Done():
				break loop
			case ev, ok := <-ch:
				if !ok {
					break loop
				}
				logging.Infof("data list changed")
				sd.diffDataID(cfgCh, ev)
			}
		}
		close(cfgCh)
	}()

	return cfgCh, nil
}

// DiffDataID :
func (sd *SourceDriver) diffDataID(cfgCh chan<- *CfgEvent, events *Event) {
	if len(events.Detail) == 0 {
		return
	}
	cfgEvent := NewCfgEvent()
	for _, ei := range events.Detail {
		subPathList := strings.Split(ei.DataPath, "/")
		if len(subPathList) == 0 {
			logging.Errorf("get invalid Root:%s", ei.DataPath)
			continue
		}
		dataIDNum, err := strconv.Atoi(subPathList[len(subPathList)-1])
		if err != nil {
			logging.Errorf("data ID %s atoi err:%s", subPathList[len(subPathList)-1], err.Error())
			continue
		}
		var ceItem CfgEventItem
		ceItem.EventType = ei.EventType
		ceItem.DataPath = ei.DataPath
		ceItem.DataValue = ei.DataValue
		ceItem.DataID = dataIDNum
		cfgEvent.Detail = append(cfgEvent.Detail, ceItem)
	}
	logging.Infof("get dataid config detail length: %d", len(cfgEvent.Detail))
	if len(cfgEvent.Detail) != 0 {
		select {
		case <-sd.ctx.Done():
			return
		case cfgCh <- cfgEvent:
			logging.Infof("data ID changed")
		}
	}
}

// StartMonitorDataID :
func StartMonitorDataID(ctx context.Context) (<-chan *CfgEvent, error) {
	// for mock convenience
	consulClient, err := NewConsulClient(ctx)
	if err != nil {
		logging.Errorf("NewConsulClient failed:%s", err.Error())
		return nil, err
	}
	sourceDriver := NewSourceDriver(ctx, consulClient)
	return sourceDriver.MonitorDataID()
}
