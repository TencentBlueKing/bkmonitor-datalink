// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"context"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestQsToDsl(t *testing.T) {
	mock.Init()

	ctx := context.Background()
	for i, c := range []struct {
		q string
	}{
		{
			q: `msg.\*: (ERROR OR INFO)`,
		},
		{
			q: `log: "ERROR MSG"`,
		},
		{
			q: `quick brown fox`,
		},
		{
			q: `quick AND brown`,
		},
		{
			q: `age:[18 TO 30]`,
		},
		{
			q: `qu?ck br*wn`,
		},
		{
			q: `(quick OR brown) AND fox`,
		},
		{
			q: `title:quick`,
		},
		{
			q: `log: /data/bkee/bknodeman/nodeman/apps/backend/subscription/tasks.py`,
		},
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			qs := NewQueryString(c.q)
			err := qs.ToDsl()
			if err != nil {
				log.Errorf(ctx, err.Error())
			}
		})
	}
}
