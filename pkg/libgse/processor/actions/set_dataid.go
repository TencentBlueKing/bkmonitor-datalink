// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package actions

import (
	"fmt"

	"github.com/elastic/beats/libbeat/beat"
	"github.com/elastic/beats/libbeat/common"
	"github.com/elastic/beats/libbeat/logp"
	"github.com/elastic/beats/libbeat/processors"
)

type dataid struct {
	dataID dataidConfig
}

type dataidConfig struct {
	DataID uint64 `config:"dataid"`
}

// init registers the add_cloud_metadata processor.
func init() {
	processors.RegisterPlugin("set_dataid", configChecked(new, requireFields("dataid"), allowedFields("dataid", "when")))
}

func new(c *common.Config) (processors.Processor, error) {
	processor := &dataid{dataID: dataidConfig{DataID: 0}}
	err := c.Unpack(&processor.dataID)
	if err != nil {
		return nil, fmt.Errorf("fail to unpack the drop_fields configuration: %s", err)
	}

	logp.Debug("bkfilters", "bkfilter config %v", processor.dataID)

	return processor, nil
}

func (cli dataid) Run(event *beat.Event) (*beat.Event, error) {
	data := event.Fields
	if ok, _ := data.HasKey("dataid"); !ok {
		data.Update(common.MapStr{"dataid": cli.dataID.DataID})
	}
	event.Fields = data
	return event, nil
}

func (cli dataid) String() string {
	return "set_dataid"
}
