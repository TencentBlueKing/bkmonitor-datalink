// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// RecoverErrorSuite
type RecoverErrorSuite struct {
	suite.Suite
}

// TestRecoverErrorSuite
func TestRecoverErrorSuite(t *testing.T) {
	suite.Run(t, new(RecoverErrorSuite))
}

// TestPanic
func (s *RecoverErrorSuite) TestPanic() {
	cases := []struct {
		object   interface{}
		activate bool
	}{
		{fmt.Errorf("test"), true},
		{"error message", true},
		{nil, false},
		{struct{}{}, true},
	}

	for i, c := range cases {
		activated := false
		s.NotPanics(func() {
			defer utils.RecoverError(func(e error) {
				activated = true
			})
			panic(c.object)
		})
		s.Equal(c.activate, activated, i)
	}
}
