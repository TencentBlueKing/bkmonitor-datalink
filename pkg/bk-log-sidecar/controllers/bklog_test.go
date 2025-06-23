// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package controllers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	ctrl "sigs.k8s.io/controller-runtime"
)

type bkLogSidecarSuite struct {
	suite.Suite
	bkLogSidecar *BkLogSidecar
}

func (s *bkLogSidecarSuite) SetupSuite() {
	s.bkLogSidecar = &BkLogSidecar{
		log: ctrl.Log.WithName("bkLogSidecar"),
	}
}

func (s *bkLogSidecarSuite) TestMatchWorkload() {

}

func (s *bkLogSidecarSuite) TestMatchWorkloadType() {

}

func (s *bkLogSidecarSuite) TestMatchWorkloadName() {

}

func (s *bkLogSidecarSuite) TestMatchContainerName() {
	containerNameTests := []struct {
		containerName        string
		containerNameMatch   []string
		containerNameExclude []string
		result               bool
	}{
		{
			"test",
			[]string{"test"},
			[]string{},
			true,
		},
		{
			"test",
			[]string{"test", "test1"},
			[]string{"test"},
			false,
		},
		{
			"test",
			[]string{},
			[]string{"test"},
			false,
		},
		{
			"test",
			[]string{},
			[]string{"test1"},
			true,
		},
		{
			"test",
			[]string{"", "tasdf", "test"},
			[]string{},
			true,
		},
		{
			"test",
			[]string{"tes"},
			[]string{},
			false,
		},
		{
			"test",
			[]string{"tes", "gdf"},
			[]string{},
			false,
		},
		{
			"test",
			[]string{"es", "gdf"},
			[]string{},
			false,
		},
	}
	for _, test := range containerNameTests {
		assert.Equal(s.T(), test.result, s.bkLogSidecar.matchContainerName(test.containerName, test.containerNameMatch, test.containerNameExclude))
	}
}

func TestBkLogSidecar(t *testing.T) {
	suite.Run(t, new(bkLogSidecarSuite))
}
