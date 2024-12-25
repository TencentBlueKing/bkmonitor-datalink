// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metricbeat

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/elastic/beats/libbeat/common"
	"github.com/elastic/go-ucfg/yaml"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

func TestMetricBeatGatherRun(t *testing.T) {
	globalConfig := configs.NewConfig()
	globalConfig.GatherUpBeat.DataID = 10001
	taskConf := configs.NewMetricBeatConfig()
	buf := []byte(`module: prometheus
metricsets: ["collector"]
enabled: true
hosts: ["http://127.0.0.1:8989/status"]
metrics_path: ''
namespace: namespace_0824
dataid: 1573267`)
	fmt.Printf("Error: %v", buf)
	ucfgConfig, err := yaml.NewConfig(buf)
	if err != nil {
		panic(fmt.Errorf("Error config ... "))
	}
	t.Logf("Initial module config: %v", ucfgConfig)
	taskConf.Module = (*common.Config)(ucfgConfig)

	gather := New(globalConfig, taskConf)
	e := make(chan define.Event, 100)
	gather.Run(context.Background(), e)
	gather.Wait()
	close(e)
	num := 0
	for ev := range e {
		event := ev.AsMapStr()
		t.Logf("Event: %v\n", event)
		num += 1
	}
	assert.Equal(t, num, 2)
}

func TestOldVerionUrlParams(t *testing.T) {
	globalConfig := configs.NewConfig()
	globalConfig.GatherUpBeat.DataID = 10001
	taskConf := configs.NewMetricBeatConfig()
	buf := []byte(`module: prometheus
metricsets: ["collector"]
enabled: true
hosts: ["http://127.0.0.1:8989/status?rrr=111&bbbbb3=testContent"]
metrics_path: ''
namespace: namespace_0824
dataid: 1573267`)
	fmt.Printf("Error: %v", buf)
	ucfgConfig, err := yaml.NewConfig(buf)
	if err != nil {
		panic(fmt.Errorf("Error config ... "))
	}
	t.Logf("Initial module config: %v", ucfgConfig)
	taskConf.Module = (*common.Config)(ucfgConfig)

	gather := New(globalConfig, taskConf)
	e := make(chan define.Event, 100)
	gather.Run(context.Background(), e)
	gather.Wait()
	close(e)
	num := 0
	result := false
	for ev := range e {
		event := ev.AsMapStr()
		if strings.Contains(event.String(), "testContent") {
			result = true
		}
		t.Logf("Event: %v\n", event)
		num += 1
	}
	assert.Equal(t, result, true)
}

func TestNewVerionUrlParams(t *testing.T) {
	globalConfig := configs.NewConfig()
	globalConfig.GatherUpBeat.DataID = 10001
	taskConf := configs.NewMetricBeatConfig()
	bufNew := []byte(`module: prometheus
metricsets: ["collector"]
enabled: true
hosts: ["http://127.0.0.1:8989/status?rrr=111&bbbbb3=testContent"]
metrics_path: ''
query: {"test":["testContent2"]}
namespace: namespace_0824
dataid: 1573267`)
	fmt.Printf("Error: %v", bufNew)
	ucfgConfig, err := yaml.NewConfig(bufNew)
	if err != nil {
		panic(fmt.Errorf("Error config ... "))
	}
	t.Logf("Initial module config: %v", ucfgConfig)
	taskConf.Module = (*common.Config)(ucfgConfig)

	gather := New(globalConfig, taskConf)
	e := make(chan define.Event, 100)
	gather.Run(context.Background(), e)
	gather.Wait()
	close(e)
	num := 0
	result := false
	for ev := range e {
		event := ev.AsMapStr()
		if strings.Contains(event.String(), "testContent2") {
			result = true
		}
		t.Logf("Event: %v\n", event)
		num += 1
	}
	assert.Equal(t, result, true)
}
