// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/TarsCloud/TarsGo/tars"
	"github.com/TarsCloud/TarsGo/tars/protocol/res/propertyf"
	"github.com/TarsCloud/TarsGo/tars/protocol/res/statf"
)

const (
	NodeIp      string = "127.0.0.1"
	ServerPort  int    = 4319
	StatServant string = "collector.tarsstat.StatObj"
	PropServant string = "collector.tarsproperty.PropertyObj"
	MockToken   string = "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw=="
)

func newPropApp() *propertyf.PropertyF {
	comm := tars.NewCommunicator()
	obj := fmt.Sprintf("%s@tcp -h %s -p %d -t 60000", PropServant, NodeIp, ServerPort)
	app := new(propertyf.PropertyF)
	comm.StringToProxy(obj, app)
	return app
}

func newStatApp() *statf.StatF {
	comm := tars.NewCommunicator()
	obj := fmt.Sprintf("%s@tcp -h %s -p %d -t 60000", StatServant, NodeIp, ServerPort)
	app := new(statf.StatF)
	comm.StringToProxy(obj, app)
	return app
}

func reportProps(f func(context.Context, map[propertyf.StatPropMsgHead]propertyf.StatPropMsgBody, ...map[string]string) (int32, error)) {
	props := map[propertyf.StatPropMsgHead]propertyf.StatPropMsgBody{
		{
			ModuleName:   "TestApp.HelloGo",
			Ip:           "127.0.0.1",
			PropertyName: "Add",
			SetName:      "",
			SetArea:      "",
			SetID:        "",
			SContainer:   "",
			IPropertyVer: 2,
		}: {VInfo: []propertyf.StatPropInfo{
			{Value: "440", Policy: "Sum"},
			{Value: "73.333", Policy: "Avg"},
			{Value: "94", Policy: "Max"},
			{Value: "33", Policy: "Min"},
			{Value: "6", Policy: "Count"},
			{Value: "0|0,50|1,100|5", Policy: "Distr"},
		}},
	}

	ctx := context.Background()
	kv := map[string]string{"X-BK-TOKEN": MockToken}
	_, err := f(ctx, props, kv)
	if err != nil {
		log.Printf("failed to invoke err=%v", err)
	}
}

func reportStats(f func(context.Context, map[statf.StatMicMsgHead]statf.StatMicMsgBody, bool, ...map[string]string) (int32, error)) {
	stats := map[statf.StatMicMsgHead]statf.StatMicMsgBody{
		{
			MasterName:    "stat_from_server",
			SlaveName:     "TestApp.HelloGo",
			InterfaceName: "Add",
			MasterIp:      "127.0.0.1",
			SlaveIp:       "127.0.0.1",
			SlavePort:     0,
			ReturnValue:   0,
			SlaveSetName:  "",
			SlaveSetArea:  "",
			SlaveSetID:    "",
			TarsVersion:   "1.4.5",
		}: {
			Count:         6,
			TimeoutCount:  0,
			ExecCount:     0,
			IntervalCount: map[int32]int32{100: 0, 200: 2, 500: 4},
			TotalRspTime:  1343,
			MaxRspTime:    284,
			MinRspTime:    159,
		},
	}

	ctx := context.Background()
	kv := map[string]string{"X-BK-TOKEN": MockToken}
	_, err := f(ctx, stats, false, kv)
	if err != nil {
		log.Printf("failed to invoke err=%v", err)
	}
}

func reportPropsNormal() {
	app := newPropApp()
	reportProps(app.ReportPropMsgWithContext)
}

func reportPropsOneway() {
	app := newPropApp()
	reportProps(app.ReportPropMsgOneWayWithContext)
}

func reportStatsNormal() {
	app := newStatApp()
	reportStats(app.ReportMicMsgWithContext)
}

func reportStatsOneway() {
	app := newStatApp()
	reportStats(app.ReportMicMsgOneWayWithContext)
}

func main() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c:
			return
		case <-ticker.C:
			reportPropsNormal()
			reportPropsOneway()
			reportStatsNormal()
			reportStatsOneway()
		}
	}
}
