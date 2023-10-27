// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

// BaseBackend :
type BaseBackend struct {
	Name           string
	PayloadCreator PayloadCreatorFunc
}

// NewBaseBackend :
func NewBaseBackend(name string) *BaseBackend {
	return NewBaseBackendWithPayloadCreator(name, NewDefaultPayload)
}

// NewBaseBackendWithPayloadCreator :
func NewBaseBackendWithPayloadCreator(name string, fn PayloadCreatorFunc) *BaseBackend {
	return &BaseBackend{
		Name:           name,
		PayloadCreator: fn,
	}
}

// String : return Backend name
func (f *BaseBackend) String() string {
	return f.Name
}

// Commit : commit check point
func (f *BaseBackend) Commit() error {
	return nil
}

// Reset : reset Backend
func (f *BaseBackend) Reset() error {
	return nil
}

// Close : close Backend
func (f *BaseBackend) Close() error {
	return nil
}

// Push : Push data
func (f *BaseBackend) Push(d Payload, killChan chan<- error) {
	panic(ErrItemNotFound)
}
