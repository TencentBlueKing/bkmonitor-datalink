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
	"strings"
	"sync"
	"time"

	"github.com/olivere/elastic/v7"
	"github.com/prometheus/prometheus/prompb"
)

const (
	MetaDataKey = "es_query_body"
)

type EsQueryBodies map[string]*EsQueryBody

type EsAggs struct {
	lock sync.RWMutex
	Aggs []*EsAgg
}

type EsAgg struct {
	Name string
	Agg  elastic.Aggregation
}

type EsQueryBody struct {
	Name        string
	TableID     string
	Field       string
	Source      []string
	QueryString string
	Query       elastic.Query
	Window      time.Duration

	Aggs     *EsAggs
	AggIndex int
	Relabel  func(lb prompb.Label) prompb.Label
}

func NewEsAggregations(length int) *EsAggs {
	return &EsAggs{
		Aggs: make([]*EsAgg, 0, length),
	}
}

func (as *EsAggs) Insert(agg *EsAgg) {
	as.lock.Lock()
	defer as.lock.Unlock()
	as.Aggs = append([]*EsAgg{agg}, as.Aggs...)
}

func (as *EsAggs) Agg() *EsAgg {
	if as != nil && as.Length() > 0 {
		return as.Aggs[0]
	}
	return &EsAgg{}
}

func (as *EsAggs) String() string {
	var s []string
	for _, a := range as.Aggs {
		s = append(s, a.Name)
	}
	return strings.Join(s, " - ")
}

func (as *EsAggs) Length() int {
	as.lock.RLock()
	defer as.lock.RUnlock()
	return len(as.Aggs)
}

func (b EsQueryBodies) Length() int {
	return len(b)
}

func (b EsQueryBodies) QueryBody(refName string) *EsQueryBody {
	if v, ok := b[refName]; ok {
		return v
	}
	return nil
}

func (b EsQueryBodies) FunctionIndex() map[string]int {
	fnIdx := make(map[string]int, b.Length())
	for name, q := range b {
		fnIdx[name] = q.AggIndex
	}
	return fnIdx
}
