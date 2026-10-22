// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	promPromql "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"
)

// A text joined from several routes keeps its words and still answers for
// each route's own error.
func TestErrorWithCausesKeepsTheTextAndEveryCause(t *testing.T) {
	timeout := fmt.Errorf("route b: %w", context.DeadlineExceeded)
	refused := errors.New("route a: parse error")
	err := ErrorWithCauses("query error: route a: parse error query error: route b: context deadline exceeded", refused, nil, timeout)

	require.Equal(t, "query error: route a: parse error query error: route b: context deadline exceeded", err.Error())
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorIs(t, err, refused)
	require.NotErrorIs(t, ErrorWithCauses("context deadline exceeded"), context.DeadlineExceeded,
		"the words alone are not a timeout")
}

func TestFailureStatus(t *testing.T) {
	cases := map[string]struct {
		err  error
		want int
	}{
		"a deadline anywhere in the chain": {err: NewMessage(MsgQueryTs, "查询异常").Error(context.Background(),
			ErrorWithCauses("query error: x", fmt.Errorf("x: %w", context.DeadlineExceeded))), want: http.StatusGatewayTimeout},
		"the query engine's own timeout": {err: fmt.Errorf("engine: %w", promPromql.ErrQueryTimeout("query execution")), want: http.StatusGatewayTimeout},
		"a caller that cancelled":        {err: fmt.Errorf("x: %w", context.Canceled), want: http.StatusBadRequest},
		"a request refused as written":   {err: errors.New("1:10: parse error"), want: http.StatusBadRequest},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, c.want, FailureStatus(c.err))
		})
	}
}
