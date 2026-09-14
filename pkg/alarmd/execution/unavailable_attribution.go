// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

// UnavailableAttribution says where an unavailable completion's reason code
// came from. The two functions that pick that code walk the attempts for one
// that names a reason and fall back to QUERY_UNAVAILABLE when none does.
//
// The fallback is the problem this exists for. QUERY_UNAVAILABLE reads as
// "the query provider was unavailable", and a reader acts on it: they go and
// look at the provider. But the same code is produced when no attempt named
// a reason, and when no attempt was made at all -- three situations, one
// word, and two of them send the reader somewhere there is nothing to find.
//
// A deployment that had just compiled a thousand new Query Groups showed
// hundreds of objects held out of detection under exactly this code, spread
// across dozens of businesses, with no HTTP error and no timeout anywhere.
// Whether that was the provider or was this fallback could not be told
// apart from outside, which is why the attribution is now counted.
type UnavailableAttribution string

const (
	// UnavailableFromAttempt: an attempt named the reason. The code means
	// what it says and the attempt's detail says more.
	UnavailableFromAttempt UnavailableAttribution = "attempt"
	// UnavailableNoAttemptReason: attempts were made and none named a
	// reason. The provider was reached, or at least tried, and what came
	// back was not classified -- the code is this function's guess.
	UnavailableNoAttemptReason UnavailableAttribution = "no_attempt_reason"
	// UnavailableNoAttempts: no attempt was made at all. Nothing was asked
	// of the provider, so nothing about the provider is being reported; the
	// query did not get as far as being sent.
	UnavailableNoAttempts UnavailableAttribution = "no_attempts"
)

// UnavailableAttributions is the closed set, for a reader that pre-creates
// one series per value so that zero is a reading.
var UnavailableAttributions = []UnavailableAttribution{
	UnavailableFromAttempt, UnavailableNoAttemptReason, UnavailableNoAttempts,
}

// AttributeUnavailable reports the reason code an unavailable completion
// should carry and where that code came from. The code is unchanged from
// what the two callers picked before; only the second result is new.
func AttributeUnavailable(facts ProviderRouteFacts, fallback ReasonCode) (ReasonCode, UnavailableAttribution) {
	for index := len(facts.Attempts) - 1; index >= 0; index-- {
		if facts.Attempts[index].ReasonCode != "" {
			return facts.Attempts[index].ReasonCode, UnavailableFromAttempt
		}
	}
	if len(facts.Attempts) == 0 {
		return fallback, UnavailableNoAttempts
	}
	return fallback, UnavailableNoAttemptReason
}
