// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package outputdropper

import (
	"github.com/elastic/beats/libbeat/beat"
	"github.com/elastic/beats/libbeat/common"
	"github.com/elastic/beats/libbeat/outputs"
	"github.com/elastic/beats/libbeat/publisher"
)

func init() {
	outputs.RegisterType(outputType, MakeDropperOutput)
}

type Output struct{}

const (
	maxBatchSize = 128
	outputType   = "dropper"
)

func MakeDropperOutput(_ outputs.IndexManager, _ beat.Info, _ outputs.Observer, _ *common.Config) (outputs.Group, error) {
	return outputs.Success(maxBatchSize, 0, &Output{})
}

func (o *Output) Close() error   { return nil }
func (o *Output) String() string { return outputType }

func (o *Output) Publish(batch publisher.Batch) error {
	batch.ACK()
	return nil
}
