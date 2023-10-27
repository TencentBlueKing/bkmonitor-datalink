// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package conv_test

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	goconv "github.com/cstockton/go-conv"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/conv"
)

// ConvSuite :
type ConvSuite struct {
	suite.Suite
}

// TestFloatToString :
func (s *ConvSuite) TestFloatToString() {
	cases := []struct {
		value  interface{}
		expect string
	}{
		{0, "0"},
		{1.0, "1"},
		{10000.0, "10000"},
		{100000000.0, "100000000"},
		{1000000000000.0, "1000000000000"},
		{10000000000000000.0, "10000000000000000"},
		{12345678912345678.0, "12345678912345678"},
		{123456789.12345678, "123456789.12345678"},
		{123456789.12345678, "123456789.12345678"},
		{10000.25, "10000.25"},
		{100000000.3, "100000000.3"},
		{1000000000000.5, "1000000000000.5"},
	}
	for i, c := range cases {
		v, err := goconv.DefaultConv.String(c.value)
		s.NoError(err)
		s.Equalf(c.expect, v, "%v", i)
	}
}

// TestFloatToStringByRange :
func (s *ConvSuite) TestFloatToStringByRange() {
	max := 1000000000000000
	for i := 0; i <= max; i += rand.Intn(1000000000000) {
		v1, err := goconv.DefaultConv.String(i)
		s.NoError(err)

		f := float64(i)
		v2, err := goconv.DefaultConv.String(f)
		s.NoError(err)

		s.Equal(v1, v2, "%d & %f", i, f)
	}
}

// TestFloatString :
func (s *ConvSuite) TestFloatString() {
	cases := []struct {
		value  float64
		expect string
	}{
		{0, "0"},
		{0.0, "0"},
		{1.0, "1"},
		{10.0, "10"},
		{10, "10"},
		{10.10, "10.1"},
		{10.1, "10.1"},
		{1000000000000.0, "1000000000000"},
		{1000000000000.25, "1000000000000.25"},
		{1000000000000.50, "1000000000000.5"},
	}
	for i, c := range cases {
		v, err := conv.DefaultConv.String(c.value)
		s.NoError(err)
		s.Equalf(c.expect, v, "%v", i)
	}
}

// TestConvSuite :
func TestConvSuite(t *testing.T) {
	suite.Run(t, new(ConvSuite))
}

// BenchmarkConverter_String :
func BenchmarkConverter_String(b *testing.B) {
	f := 1000000000000.50
	for i := 0; i < b.N; i++ {
		conv.DefaultConv.String(f)
	}
}

func xString(from interface{}) (value string, err error) {
	switch from.(type) {
	case float32, float64:
		value = fmt.Sprintf("%.8f", from)
	default:
		return fmt.Sprintf("%vf", from), nil
	}
	finished := false
	return strings.TrimRightFunc(value, func(r rune) bool {
		if finished {
			return false
		} else if r == '0' {
			return true
		} else if r == '.' {
			finished = true
			return true
		}
		return false
	}), nil
}

// BenchmarkConverter_XString :
func BenchmarkConverter_XString(b *testing.B) {
	f := 1000000000000.50
	for i := 0; i < b.N; i++ {
		xString(f)
	}
}
