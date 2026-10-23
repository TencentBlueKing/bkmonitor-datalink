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
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// IdentityUnmatchedFacts is the evidence of a KindIdentityUnmatched row: how
// many rounds in a row the data's identity labels could not be read by the
// target, the latest round's withheld series by reason, a few series'
// identity fields (names and values only), and the line written from them.
type IdentityUnmatchedFacts struct {
	Rounds        int                            `json:"rounds"`
	Withheld      uint64                         `json:"withheld"`
	OutsideTarget uint64                         `json:"outside_target"`
	KeyMissing    uint64                         `json:"key_missing"`
	Samples       []observability.IdentitySample `json:"samples"`
	Line          string                         `json:"line"`
}

func identityUnmatchedOf(rounds int, facts *observability.IdentityFacts) *IdentityUnmatchedFacts {
	out := &IdentityUnmatchedFacts{Rounds: rounds, Withheld: facts.Withheld, OutsideTarget: facts.OutsideTarget,
		KeyMissing: facts.KeyMissing, Samples: append([]observability.IdentitySample{}, facts.Samples...)}
	out.Line = identityUnmatchedLine(out.Samples)
	return out
}

// identityUnmatchedLine is the row's sentence from its first sample: the
// field whose value the strategy requires and the value the data carries, or
// the key field the data does not carry.
func identityUnmatchedLine(samples []observability.IdentitySample) string {
	const lead = "数据在，但身份标签对不上"
	if len(samples) == 0 {
		return lead
	}
	for _, field := range samples[0].Fields {
		if field.Canonical || field.Expected == "" || field.Present && field.Actual == field.Expected {
			continue
		}
		if !field.Present {
			return lead + "：策略要 " + field.Name + "=" + field.Expected + "，数据里没有 " + field.Name
		}
		return lead + "：策略要 " + field.Name + "=" + field.Expected + "，数据里是 " + field.Name + "=" + field.Actual
	}
	missing := []string{}
	for _, field := range samples[0].Fields {
		if !field.Canonical && !field.Present {
			missing = append(missing, field.Name)
		}
	}
	if len(missing) > 0 {
		return lead + "：策略要读 " + strings.Join(missing, "、") + "，数据里没有"
	}
	return lead
}
