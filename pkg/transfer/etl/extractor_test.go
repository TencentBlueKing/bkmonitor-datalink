// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package etl_test

import (
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// ExtractByPathSuite :
type ExtractByPathSuite struct {
	suite.Suite
}

// TestUsage :
func (s *ExtractByPathSuite) TestUsage() {
	cases := []struct {
		putPath, getPath   []string
		putValue, getValue interface{}
		err                error
	}{
		{[]string{"x"}, []string{"x"}, 0, 0, nil},
		{[]string{"x", "y", "z"}, []string{"x", "y", "z"}, 1, 1, nil},
		{[]string{"x"}, []string{"x", "y"}, 2, nil, etl.ErrExtractTypeUnknown},
		{[]string{"x", "y"}, []string{"x", "y", "z"}, make(map[string]int), nil, etl.ErrExtractTypeUnknown},
		{[]string{"x", "y"}, []string{"x", "y", "z"}, map[string]interface{}{"z": 1}, nil, etl.ErrExtractTypeUnknown},
		{[]string{"x"}, []string{"x"}, define.ErrItemNotFound, nil, define.ErrItemNotFound},
	}

	for _, c := range cases {
		ctrl := gomock.NewController(s.T())
		container := NewMockContainer(ctrl)
		nextContainer := container
		for _, p := range c.putPath[:len(c.putPath)-1] {
			subContainer := NewMockContainer(ctrl)
			nextContainer.EXPECT().Get(p).Return(subContainer, nil)
			nextContainer = subContainer
		}
		switch c.putValue.(type) {
		case error:
			nextContainer.EXPECT().Get(c.putPath[len(c.putPath)-1]).Return(nil, c.putValue)
		default:
			nextContainer.EXPECT().Get(c.putPath[len(c.putPath)-1]).Return(c.putValue, nil)
		}

		extractor := etl.ExtractByPath(c.getPath...)
		value, err := extractor(container)

		s.Equal(c.getValue, value)
		s.Equal(c.err, err)
		ctrl.Finish()
	}
}

// TestExtractByPath :
func TestExtractByPath(t *testing.T) {
	suite.Run(t, new(ExtractByPathSuite))
}
