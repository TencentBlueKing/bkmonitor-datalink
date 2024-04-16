// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package segmented

import (
	"sync"
)

var (
	sectionPool sync.Pool
)

type section struct {
	time int64
	fn   func(s *section) interface{}

	next *section
}

func newSection() interface{} {
	return &section{}
}

func (s *section) zero() {
	s.fn = nil
	s.next = nil
}

func (s *section) Recycle() {
	s.zero()
	sectionPool.Put(s)
}

func init() {
	sectionPool.New = newSection
}
