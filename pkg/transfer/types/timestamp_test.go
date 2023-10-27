// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package types_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

// TimeStampSuite :
type TimeStampSuite struct {
	suite.Suite
}

// TestUsage :
func (s *TimeStampSuite) TestString() {
	layout := time.RFC3339
	cases := []struct {
		input  string
		output string
	}{
		{"2019-01-17T11:24:00+08:00", "1547695440"},
		{"2019-01-17T03:51:03+00:00", "1547697063"},
	}

	for _, c := range cases {
		t, err := time.Parse(layout, c.input)
		s.NoError(err)
		s.Equal(c.output, fmt.Sprintf("%d", t.Unix()))
	}
}

// TestTimeStampSuite :
func TestTimeStampSuite(t *testing.T) {
	suite.Run(t, new(TimeStampSuite))
}
