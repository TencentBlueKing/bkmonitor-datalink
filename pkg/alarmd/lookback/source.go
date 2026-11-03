// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package lookback measures when each source's data is complete. Every Query
// Group this process owns keeps one sample of a formal first read, as a
// summary per bucket; the same window is read again at rungs after it, each
// read compared with the one before, and the last rung that still changed
// is when that window's data was complete. How deep the rungs go and how
// often a Query Group is sampled follow what each source's data actually
// does. It never writes State, Progress or an event: what it produces is
// counts, a few bounded examples, and per Query Group the last measurement.
package lookback

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Source labels besides the data sources a query can be compiled from: a
// query reading several of them, and one the list does not name.
const (
	SourceMixed = controlplane.SourceSemanticsMixed
	SourceOther = controlplane.SourceSemanticsOther
)

// Sources is every source label, each counted from the start: the data
// sources, mixed and other.
var Sources = append(append([]string{}, controlplane.SupportedSourceSemantics...), SourceMixed, SourceOther)

// sourceOf labels the data source a query reads as the catalog labels its
// Query Group, so a source's lateness reads beside its Query Groups: a plan
// with no semantics is plain time series, a PromQL query reads the source
// its plan names, and one label the list does not name is other.
func sourceOf(facts execution.QueryPlanFacts) string {
	return controlplane.SourceSemanticsLabel(facts.SourceSemantics)
}
