// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import (
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// A series' delivery digest, assembled with the encoding of the identity its
// identity digest was derived with, is the generic canonical digest of the
// records the series delivered.
func TestASeriesDeliveryDigestTakesItsIdentitysEncoding(t *testing.T) {
	names := make([]string, 10)
	values := make([]string, 10)
	for index := range names {
		names[index] = fmt.Sprintf("label_%d", index)
		values[index] = fmt.Sprintf("value-%d", index)
	}
	attempt := identityAttempt(t, names...)
	batch, _, err := normalizeSeries(attempt.Spec, "provider-result", identityTestSeries(names, values), 1_700_123_500)
	if err != nil {
		t.Fatalf("normalizeSeries() error = %v", err)
	}
	records := batch.Dataset.Records()
	if len(records) != 1 || len(records[0].DimensionIdentity.Fields) != len(names) {
		t.Fatalf("records = %+v, want one record carrying the ten identity fields", records)
	}
	want, err := contract.DeriveCanonicalDigestV2("alarmd-provider-series-delivery-v1", records)
	if err != nil || batch.Delivery.Digest != want {
		t.Fatalf("delivery digest = %s, canonical %s (%v)", batch.Delivery.Digest, want, err)
	}
}
