// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package es

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corees "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/es"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

func TestValidateTimeRange(t *testing.T) {
	t.Setenv(MaxQueryTimeRangeEnv, "168h")
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.Local)

	testCases := []struct {
		name string
		end  time.Time
		err  error
	}{
		{name: "exact maximum", end: start.Add(7 * 24 * time.Hour)},
		{name: "over maximum", end: start.Add(7*24*time.Hour + time.Second), err: ErrTimeRangeTooLarge},
		{name: "empty range", end: start, err: ErrInvalidTimeRange},
		{name: "reversed range", end: start.Add(-time.Second), err: ErrInvalidTimeRange},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateTimeRange(&Params{Start: start.Unix(), End: testCase.end.Unix()})
			if testCase.err == nil {
				require.NoError(t, err)
				return
			}
			assert.True(t, errors.Is(err, testCase.err), err)
		})
	}
	assert.ErrorIs(t, validateTimeRange(nil), ErrInvalidTimeRange)
}

func TestMaxQueryTimeRangeFromEnvironment(t *testing.T) {
	t.Setenv(MaxQueryTimeRangeEnv, "48h")
	assert.Equal(t, 48*time.Hour, maxQueryTimeRange())

	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	err := validateTimeRange(&Params{Start: start.Unix(), End: start.Add(48*time.Hour + time.Second).Unix()})
	assert.ErrorIs(t, err, ErrTimeRangeTooLarge)
}

func TestMaxQueryTimeRangeFromLegacyEnvironment(t *testing.T) {
	t.Setenv(MaxQueryTimeRangeEnv, "")
	t.Setenv(legacyMaxQueryTimeRangeEnv, "24h")
	assert.Equal(t, 24*time.Hour, maxQueryTimeRange())
}

func TestMaxQueryTimeRangeFallsBackForInvalidValue(t *testing.T) {
	t.Setenv(MaxQueryTimeRangeEnv, "not-a-duration")
	assert.Equal(t, defaultMaxQueryTimeRange, maxQueryTimeRange())
}

func TestFormatQueryTargetsIncludesCrossBoundaryEnd(t *testing.T) {
	log.InitTestLogger()
	start := time.Date(2024, 1, 1, 23, 30, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	info := &corees.TableInfo{
		AliasFormat: "{index}_{time}_read",
		DateFormat:  "20060102",
		DateStep:    120,
	}

	aliases, err := formatQueryTargets(info, &Params{
		TableID: "testbb.ttt",
		Start:   start.Unix(),
		End:     end.Unix(),
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"testbb_ttt_20240101*_read", "testbb_ttt_20240102*_read"}, aliases)

	fuzzyAliases, err := formatQueryTargets(info, &Params{
		TableID:       "testbb.ttt",
		Start:         start.Unix(),
		End:           end.Unix(),
		FuzzyMatching: true,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"testbb_ttt_20240101*",
		"v2_testbb_ttt_20240101*",
		"testbb_ttt_20240102*",
		"v2_testbb_ttt_20240102*",
	}, fuzzyAliases)
}

func TestFormatQueryTargetsRejectsInvalidFormats(t *testing.T) {
	_, err := formatQueryTargets(&corees.TableInfo{}, &Params{})
	assert.ErrorIs(t, err, ErrInvalidDateFormat)

	_, err = formatQueryTargets(&corees.TableInfo{DateFormat: "2006010215"}, &Params{})
	assert.ErrorIs(t, err, ErrInvalidDateFormat)

	_, err = formatQueryTargets(&corees.TableInfo{DateFormat: "20060102"}, &Params{})
	assert.ErrorIs(t, err, ErrInvalidAliasFormat)
}
