// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package beat

import (
	"flag"
	"time"

	"github.com/elastic/beats/libbeat/beat"
	"github.com/elastic/beats/libbeat/common"

	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/monitoring/report/bkpipe"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/output/bkpipe"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/output/bkpipe_multi"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/output/gse"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/output/logpush"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/output/otlp"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/processor/actions"
)

type MapStr = common.MapStr

type Event = beat.Event

type PublishConfig = beat.ClientConfig

type ProcessingConfig = beat.ProcessingConfig

type PublishMode = beat.PublishMode

type ClientEventer = beat.ClientEventer

type EventMetadata = common.EventMetadata

type MapStrPorinter = common.MapStrPointer

type ProcessorList = beat.ProcessorList

type Processor = beat.Processor

const (
	DefaultGuarantees = beat.DefaultGuarantees
	GuaranteedSend    = beat.GuaranteedSend
	DropIfFull        = beat.DropIfFull
)

// ReloadChan indicates developers to reload config when new config is ready
var (
	ReloadChan chan bool
	Done       chan bool
)

var (
	reloadFlag      = flag.Bool("reload", false, "Reload the program")
	testMode        = flag.Bool("T", false, "TestMode is for testing purpose which will only run task once")
	gseCheck        = flag.Bool("gse-check", false, "If present, checking gse connection then exit")
	isContainerMode = flag.Bool("container", false, "Running as container mode")
)

func IsContainerMode() bool {
	return *isContainerMode
}

func bkEventToEvent(data MapStr) beat.Event {
	ev := beat.Event{
		Fields:    data,
		Timestamp: time.Now(),
	}
	return ev
}

func formatEvent(event beat.Event) beat.Event {
	if event.Fields == nil {
		return event
	}

	if _, ok := event.Fields["time"]; !ok {
		event.Timestamp = time.Now()
		event.Fields.Put("time", event.Timestamp.Format("2006-01-02 15:04:05"))
	}

	return event
}
