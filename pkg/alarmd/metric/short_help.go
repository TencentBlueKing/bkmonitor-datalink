// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// A family's Help is written for the reader who has to act on it: what the
// family counts, then what each label value means and what to make of it.
// The CLI's metrics.list and the reference beside this file (METRICS.md,
// generated from the registry) carry it whole. A scrape carries only the
// first sentence: whole, the Help was about 140 KB of every scrape of every
// replica, and nothing that stores a scrape reads it.

// MaxShortHelpBytes bounds the sentence a scrape carries for one family.
// TestEveryServedHelpIsOneShortSentence holds every family to it, so a long
// first sentence is shortened where it is written rather than cut here.
const MaxShortHelpBytes = 320

// ShortHelp is the first sentence of help: up to the first full stop or
// colon outside brackets that a space follows, ended with a full stop.
// "e.g.", "i.e." and "vs." do not end it, and a help with no such stop is
// its own first sentence.
func ShortHelp(help string) string {
	depth := 0
	for i := 0; i < len(help); i++ {
		switch help[i] {
		case '(', '[':
			depth++
		case ')', ']':
			if depth > 0 {
				depth--
			}
		case '.', ':':
			if depth > 0 || i+1 == len(help) || help[i+1] != ' ' {
				continue
			}
			if help[i] == ':' {
				return help[:i] + "."
			}
			if before := help[:i]; strings.HasSuffix(before, "e.g") || strings.HasSuffix(before, "i.e") ||
				before == "vs" || strings.HasSuffix(before, " vs") {
				continue
			}
			return help[:i+1]
		}
	}
	return help
}

// ScrapeGatherer is the registry as /metrics serves it: every family with
// its ShortHelp, everything else as gathered. Gatherer stays the whole
// registry, Help included, for the CLI.
func (r *Recorder) ScrapeGatherer() prometheus.Gatherer {
	return shortHelpGatherer{gatherer: r.registry, short: &sync.Map{}}
}

// shortHelpGatherer keeps each Help's short form, keyed by the Help itself:
// a family's Help does not change while the process runs, so after the
// first scrape a scrape allocates nothing for it.
type shortHelpGatherer struct {
	gatherer prometheus.Gatherer
	short    *sync.Map
}

func (g shortHelpGatherer) Gather() ([]*dto.MetricFamily, error) {
	families, err := g.gatherer.Gather()
	for _, family := range families {
		if family.Help == nil {
			continue
		}
		short, found := g.short.Load(*family.Help)
		if !found {
			text := ShortHelp(*family.Help)
			short, _ = g.short.LoadOrStore(*family.Help, &text)
		}
		family.Help = short.(*string)
	}
	return families, err
}
