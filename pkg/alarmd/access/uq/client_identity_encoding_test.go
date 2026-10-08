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

// A series' identity fields are encoded once: the delivery digest takes
// their encoding from the identity the series' identity digest was derived
// with. One series normalized here is one delivery digest counted as taking
// the identity's encoding, and none as encoding it again -- and the digest is
// the generic canonical digest of the records the series delivered.
func TestASeriesDeliveryDigestTakesItsIdentitysEncoding(t *testing.T) {
	names := make([]string, 10)
	values := make([]string, 10)
	for index := range names {
		names[index] = fmt.Sprintf("label_%d", index)
		values[index] = fmt.Sprintf("value-%d", index)
	}
	attempt := identityAttempt(t, names...)
	reused, encoded := contract.ReadIdentityPartCounts()
	batch, _, err := normalizeSeries(attempt.Spec, "provider-result", identityTestSeries(names, values), 1_700_123_500)
	if err != nil {
		t.Fatalf("normalizeSeries() error = %v", err)
	}
	if nowReused, nowEncoded := contract.ReadIdentityPartCounts(); nowReused != reused+1 || nowEncoded != encoded {
		t.Fatalf("identity parts: reused %d -> %d, encoded %d -> %d; want one reused and none encoded again",
			reused, nowReused, encoded, nowEncoded)
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
