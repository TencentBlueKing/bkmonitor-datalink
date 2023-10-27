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
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/types"
)

// TimeTransformSuite :
type TimeTransformSuite struct {
	suite.Suite
}

// TestTransformTimeStampByName :
func (s *TimeTransformSuite) TestTransformTimeStampByName() {
	cases := []struct {
		name   string
		zone   int
		value  interface{}
		expect int64
	}{
		{"rfc3339_nano", -7, "2019-10-19T03:24:25.313404-07:00", 1571480665},
		{"rfc3339_nano", 8, "2019-10-19T18:07:19.081164+08:00", 1571479639},
		{"date", 5, "2019-10-19", 1571425200},
	}

	for i, c := range cases {
		fn := etl.TransformTimeStampByName(c.name, c.zone)
		result, err := fn(c.value)
		s.NoError(err, i)

		ts, ok := result.(types.TimeStamp)
		s.True(ok, i)
		s.Equal(c.expect, ts.Int64(), i)
	}
}

// TestTransformNumberToTime
func (s *TimeTransformSuite) TestTransformNumberToTime() {
	now := time.Now()
	cases := []int64{
		1,           // epoch_nanosecond
		1000,        // epoch_microsecond
		1000000,     // epoch_millisecond,
		1000000000,  // epoch_second
		60000000000, // epoch_minute
	}

	for i, c := range cases {
		ts := now.UnixNano() / c
		result, err := etl.TransformNumberToTime(ts)
		s.NoError(err)
		tm, ok := result.(time.Time)
		s.Truef(ok, "%d:%v", i, now)
		s.Equalf(ts, tm.UnixNano()/c, "%d:%v", i, now)
	}
}

// TestTransformTimeStampStringByName :
func (s *TimeTransformSuite) TestTransformTimeStampStringByName() {
	ts := types.NewTimeStamp(time.Now().UTC())
	cases := []struct {
		name   string
		expect string
	}{
		{"rfc3339_nano", ts.Format(time.RFC3339Nano)},
		{"rfc3339", ts.Format(time.RFC3339)},
		{"timestamp", ts.String()},
	}

	for i, c := range cases {
		fn := etl.TransformTimeStampStingByName(c.name)
		result, err := fn(ts)
		s.NoError(err, i)

		s.Equal(c.expect, result, i)
	}
}

// TestTransformTimeStingByLayout
func (s *TimeTransformSuite) TestTransformTimeStingByLayout() {
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04:05.000",
		"2006-01-02 15:04:05,000",
		"01/02/2006 15:04:05",
	}

	now := time.Now()
	for _, layout := range layouts {
		s.T().Log("layout:", layout, "time:", now.UTC().Format(layout))
		fn := etl.TransformTimeStingByLayout(layout)
		cases := []interface{}{
			now,
			now.UnixNano(),
		}
		for i, value := range cases {
			result, err := fn(value)
			s.NoError(err, i)
			s.Equal(now.UTC().Format(layout), result, i)
		}
	}
}

// TestTimeTransformSuite :
func TestTimeTransformSuite(t *testing.T) {
	suite.Run(t, new(TimeTransformSuite))
}
