// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"time"

	"github.com/pkg/errors"
)

// BaseDataProcessor :
type BaseDataProcessor struct {
	Name           string
	DisabledBizIDs map[string]struct{}
	baseIndex      int
	poll           time.Duration
}

// String : return frontend name
func (f *BaseDataProcessor) String() string {
	return f.Name
}

// Process : process data
func (f *BaseDataProcessor) Process(p Payload, outputChan chan<- Payload, killChan chan<- error) {
	killChan <- errors.Wrapf(ErrNotImplemented, "method Process of processor %s", f.String())
}

// Finish : process finished
func (f *BaseDataProcessor) Finish(outputChan chan<- Payload, killChan chan<- error) {
}

func (f *BaseDataProcessor) SetIndex(index int) {
	f.baseIndex = index
}

func (f *BaseDataProcessor) Index() int {
	return f.baseIndex
}

func (f *BaseDataProcessor) SetPoll(poll time.Duration) {
	f.poll = poll
}

func (f *BaseDataProcessor) Poll() time.Duration {
	return f.poll
}

// NewBaseDataProcessor :
func NewBaseDataProcessor(name string) *BaseDataProcessor {
	return &BaseDataProcessor{
		Name: name,
	}
}

// NewBaseDataProcessorWith :
func NewBaseDataProcessorWith(name string, disabledBizIDs map[string]struct{}) *BaseDataProcessor {
	return &BaseDataProcessor{
		Name:           name,
		DisabledBizIDs: disabledBizIDs,
	}
}
