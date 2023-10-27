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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
)

// BenchmarkExtractByJMESPath :
func BenchmarkExtractByJMESPath(b *testing.B) {
	b.StopTimer()
	extractors := []etl.ExtractFn{
		etl.ExtractByJMESPath(`a.b.c[0].d[1][0]`),
		etl.ExtractByJMESPath(`a`),
		etl.ExtractByJMESPath(`a.b.c.d`),
		etl.ExtractByJMESPath(`people[*].first | [0]`),
		etl.ExtractByJMESPath(`expensive`),
		etl.ExtractByJMESPath(`store.book[0].price`),
		etl.ExtractByJMESPath(`store.book[-1].isbn`),
		etl.ExtractByJMESPath(`store.book[0:2].price`),
		etl.ExtractByJMESPath(`store.book[0:3].price`),
		etl.ExtractByJMESPath(`store.book[?isbn].price`),
		etl.ExtractByJMESPath("store.book[?price > `10`].title"),
		etl.ExtractByJMESPath(`store.book[*].price`),
	}

	for i := 0; i < b.N; i++ {
		benchmarkExtractor(b, extractors)
	}
}
