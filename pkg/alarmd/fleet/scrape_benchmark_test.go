// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// BenchmarkFleetVerdictScrape is the fleet side of one verdict scrape at a
// deployment of each size, every replica holding the benchmark's 700
// objects and 450 rows, three ways:
//
//   - snapshots: every replica's whole snapshot decoded, its rows decided as
//     published, aggregated, and the rows counted -- what the scrape read
//     before it read the summaries;
//   - summaries: every replica's summary decoded, aggregated, and the check
//     lines read from the merged part -- what it reads now;
//   - fallback: the same summaries from a build before the counts, so every
//     replica is read from its snapshot and summarized one at a time -- the
//     rollout's worst case.
//
// bytes-read is the encoded payload one scrape reads.
func BenchmarkFleetVerdictScrape(b *testing.B) {
	const stallAfter = 10 * time.Minute
	for _, replicas := range []int{4, 32, 138, 210} {
		snapshots, summaries, oldSummaries := make([][]byte, 0, replicas), make([][]byte, 0, replicas), make([][]byte, 0, replicas)
		names, expected := make([]string, 0, replicas), []string{}
		for index := 0; index < replicas; index++ {
			snapshot := benchReplica(index)
			owned := benchOwned(snapshot)
			summary := SummaryOf(snapshot, owned, stallAfter)
			encodedSummary, err := json.Marshal(summary)
			if err != nil {
				b.Fatal(err)
			}
			summary.Part.Metrics = nil
			encodedOld, err := json.Marshal(summary)
			if err != nil {
				b.Fatal(err)
			}
			snapshot.OwnedObjects = owned
			encodedSnapshot, err := json.Marshal(snapshot)
			if err != nil {
				b.Fatal(err)
			}
			snapshots, summaries, oldSummaries = append(snapshots, encodedSnapshot), append(summaries, encodedSummary), append(oldSummaries, encodedOld)
			names, expected = append(names, snapshot.Replica), append(expected, owned...)
		}
		expectation := Expectation{QueryGroups: len(expected), Known: true, IDs: expected}
		digest := DigestOf(expected)
		unread := func([]string) ([][]string, bool) { return nil, false }
		size := func(payloads [][]byte) int {
			total := 0
			for _, payload := range payloads {
				total += len(payload)
			}
			return total
		}
		b.Run(fmt.Sprintf("snapshots/replicas=%d", replicas), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				decoded := make([]Snapshot, len(snapshots))
				for index, payload := range snapshots {
					if err := json.Unmarshal(payload, &decoded[index]); err != nil {
						b.Fatal(err)
					}
				}
				_ = scrapeCountsOfRows(rowsAsPublished(expectation, decoded, names, now, stallAfter), now)
			}
			b.ReportMetric(float64(size(snapshots)), "bytes-read")
		})
		b.Run(fmt.Sprintf("summaries/replicas=%d", replicas), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				decoded := make([]ReplicaSummary, len(summaries))
				for index, payload := range summaries {
					if err := json.Unmarshal(payload, &decoded[index]); err != nil {
						b.Fatal(err)
					}
				}
				view, part := AggregateSummaries(expectation, digest, decoded, names, now, freshness, unread)
				_ = CheckLines(&view, part, now)
			}
			b.ReportMetric(float64(size(summaries)), "bytes-read")
		})
		b.Run(fmt.Sprintf("fallback/replicas=%d", replicas), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				decoded := make([]ReplicaSummary, 0, len(oldSummaries))
				for _, payload := range oldSummaries {
					var summary ReplicaSummary
					if err := json.Unmarshal(payload, &summary); err != nil {
						b.Fatal(err)
					}
					decoded = append(decoded, summary)
				}
				// Every summary lacks the counts: each replica is read from its
				// snapshot, summarized and let go before the next.
				fresh := decoded[:0]
				for _, payload := range snapshots {
					var snapshot Snapshot
					if err := json.Unmarshal(payload, &snapshot); err != nil {
						b.Fatal(err)
					}
					fresh = append(fresh, summaryFromSnapshot(snapshot, stallAfter))
				}
				view, part := AggregateSummaries(expectation, digest, fresh, names, now, freshness, unread)
				_ = CheckLines(&view, part, now)
			}
			b.ReportMetric(float64(size(oldSummaries)+size(snapshots)), "bytes-read")
		})
	}
}
