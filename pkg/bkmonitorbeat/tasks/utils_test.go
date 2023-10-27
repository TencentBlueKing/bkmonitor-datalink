// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tasks

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

// BufferBuilderSuite :
type BufferBuilderSuite struct {
	suite.Suite
}

// TestBufferBuilderSuite :
func TestBufferBuilderSuite(t *testing.T) {
	suite.Run(t, &BufferBuilderSuite{})
}

// TestGetBuffer :
func (s *BufferBuilderSuite) TestGetBuffer() {
	cases := []struct {
		size1, size2, real int
	}{
		{0, 0, 0},
		{0, 1, 0},
		{1, 0, 1},
		{1, 1, 1},
	}
	for _, c := range cases {
		buffer := NewBufferBuilder()
		s.Equal(buffer.GetBuffer(c.size1), buffer.GetBuffer(c.size2))
		s.Len(buffer.GetBuffer(-1), c.real)
	}
}
