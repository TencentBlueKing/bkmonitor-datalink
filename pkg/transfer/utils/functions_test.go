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
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// ExponentialRetrySuite
type ExponentialRetrySuite struct {
	suite.Suite
}

// TestExponentialRetry
func (s *ExponentialRetrySuite) TestExponentialRetry() {
	cases := []struct {
		reties, times int
		recovered     bool
	}{
		{0, 1, true},
		{1, 2, true},
		{1, 2, false},
	}

	for i, c := range cases {
		times := 0
		err := utils.ExponentialRetry(c.reties, func() error {
			times++
			if c.recovered && times > c.reties {
				return nil
			}
			return fmt.Errorf("test")
		})
		if c.recovered {
			s.NoError(err, i)
		} else {
			s.Error(err, "test", i)
		}
		s.Equal(c.times, times, i)
	}
}

// TestContextExponentialRetry
func (s *ExponentialRetrySuite) TestContextExponentialRetry() {
	cases := []struct {
		maxs, times int
		abort       bool
	}{
		{0, 1, false},
		{1, 2, false},
		{1, 2, true},
	}

	for i, c := range cases {
		times := 0
		ctx, cancel := context.WithCancel(context.Background())
		err := utils.ContextExponentialRetry(ctx, func() error {
			times++
			if times > c.maxs {
				if c.abort {
					cancel()
				} else {
					return nil
				}
			}
			return fmt.Errorf("test")
		})
		if c.abort {
			s.Error(err, "test", i)
		} else {
			cancel()
			s.NoError(err, i)
		}
		s.Equal(c.times, times)
	}
}

// TestExponentialRetry
func TestExponentialRetry(t *testing.T) {
	suite.Run(t, new(ExponentialRetrySuite))
}
