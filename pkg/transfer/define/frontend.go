// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

// BaseFrontend :
type BaseFrontend struct {
	Name           string
	PayloadCreator PayloadCreatorFunc
}

// NewBaseFrontend :
func NewBaseFrontend(name string) *BaseFrontend {
	return NewBaseFrontendWithPayloadCreator(name, NewDefaultPayload)
}

// NewBaseFrontendWithPayloadCreator :
func NewBaseFrontendWithPayloadCreator(name string, fn PayloadCreatorFunc) *BaseFrontend {
	return &BaseFrontend{
		Name:           name,
		PayloadCreator: fn,
	}
}

// String : return frontend name
func (f *BaseFrontend) String() string {
	return f.Name
}

// Commit : commit check point
func (f *BaseFrontend) Commit() error {
	return nil
}

// Reset : reset frontend
func (f *BaseFrontend) Reset() error {
	return nil
}

// Close : close frontend
func (f *BaseFrontend) Close() error {
	return nil
}

// Pull : pull data
func (f *BaseFrontend) Pull(outputChan chan<- Payload, killChan chan<- error) {
	panic(ErrItemNotFound)
}

func (f *BaseFrontend) Flow() int { return 0 }
