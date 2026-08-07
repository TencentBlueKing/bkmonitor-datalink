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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"sync/atomic"
	"time"

	elastic "github.com/olivere/elastic/v7"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// PreparedFieldMetadata is an immutable, member-scoped snapshot of the index
// targets and mapping-derived fields required to prepare one raw query.
type PreparedFieldMetadata struct {
	indexes         []string
	physicalIndexes []string
	fieldMap        metadata.FieldsMap
	connectionKey   RawBatchConnectionKey
	reuseIdentity   [sha256.Size]byte
	complete        bool
}

// FieldsMap returns a deep copy of the mapping-derived field metadata.
func (p *PreparedFieldMetadata) FieldsMap() metadata.FieldsMap {
	if p == nil {
		return nil
	}
	return cloneFieldsMap(p.fieldMap)
}

// PreparedRawQuery owns all mutable query and formatter state for one logical
// result-table member. It must not be shared by multiple terminal schedulers.
type PreparedRawQuery struct {
	query         *metadata.Query
	queryOption   *queryOption
	fieldMetadata *PreparedFieldMetadata
	fact          *FormatFactory
	source        *elastic.SearchSource
	countQuery    elastic.Query
	body          string
	connectionKey RawBatchConnectionKey
	claimed       atomic.Bool
}

// FieldsMap returns a deep copy so highlight collection cannot mutate the
// member-owned mapping snapshot.
func (p *PreparedRawQuery) FieldsMap() metadata.FieldsMap {
	if p == nil || p.fieldMetadata == nil {
		return nil
	}
	return cloneFieldsMap(p.fieldMetadata.fieldMap)
}

// PrepareRawFieldMetadata resolves one member's aliases and mapping once.
// Empty mappings and empty physical-index sets are valid complete snapshots.
func (i *Instance) PrepareRawFieldMetadata(
	ctx context.Context,
	query *metadata.Query,
	start, end time.Time,
) (prepared *PreparedFieldMetadata, err error) {
	defer func() {
		if r := recover(); r != nil {
			prepared = nil
			err = fmt.Errorf("es query error: %s", r)
		}
	}()

	if err = i.checkQuery(query); err != nil {
		return nil, err
	}

	indexes, err := i.getAlias(ctx, query, start, end)
	if err != nil {
		return nil, err
	}
	reuseIdentity, err := rawFieldMetadataReuseIdentity(indexes, query.FieldAlias)
	if err != nil {
		return nil, err
	}
	fieldMap, physicalIndexes, err := i.fieldMapWithPhysicalIndexes(ctx, query.FieldAlias, indexes...)
	if err != nil {
		return nil, metadata.NewMessage(
			metadata.MsgQueryES,
			"字段查询异常: %+v",
			indexes,
		).Error(ctx, err)
	}

	return &PreparedFieldMetadata{
		indexes:         append([]string(nil), indexes...),
		physicalIndexes: append([]string(nil), physicalIndexes...),
		fieldMap:        cloneFieldsMap(fieldMap),
		connectionKey:   i.RawBatchConnectionKey(ctx),
		reuseIdentity:   reuseIdentity,
		complete:        true,
	}, nil
}

// PrepareRawQuery builds the final member-owned SearchSource without issuing
// the search request. A complete prefetch avoids a second alias/mapping lookup.
func (i *Instance) PrepareRawQuery(
	ctx context.Context,
	query *metadata.Query,
	start, end time.Time,
	prefetched *PreparedFieldMetadata,
) (prepared *PreparedRawQuery, err error) {
	defer func() {
		if r := recover(); r != nil {
			prepared = nil
			err = fmt.Errorf("es query error: %s", r)
		}
	}()

	if err = i.checkQuery(query); err != nil {
		return nil, err
	}

	fieldMetadata := clonePreparedFieldMetadata(prefetched)
	if fieldMetadata != nil && fieldMetadata.complete {
		expectedIndexes, aliasErr := i.getAlias(ctx, query, start, end)
		expectedIdentity, identityErr := rawFieldMetadataReuseIdentity(expectedIndexes, query.FieldAlias)
		if aliasErr != nil ||
			identityErr != nil ||
			fieldMetadata.connectionKey != i.RawBatchConnectionKey(ctx) ||
			fieldMetadata.reuseIdentity != expectedIdentity {
			fieldMetadata = nil
		}
	}
	if fieldMetadata == nil || !fieldMetadata.complete {
		fieldMetadata, err = i.PrepareRawFieldMetadata(ctx, query, start, end)
		if err != nil {
			return nil, err
		}
	}

	rawQuery := cloneRawMetadataQuery(query)
	if i.maxSize > 0 && rawQuery.Size > i.maxSize {
		rawQuery.Size = i.maxSize
	}
	if rawQuery.ResultTableOption != nil && rawQuery.ResultTableOption.From != nil {
		rawQuery.From = *rawQuery.ResultTableOption.From
	}

	qo := &queryOption{
		indexes:         append([]string(nil), fieldMetadata.indexes...),
		physicalIndexes: append([]string(nil), fieldMetadata.physicalIndexes...),
		start:           start,
		end:             end,
		query:           rawQuery,
		conn:            i.connect,
	}
	fact := newRawFormatFactory(ctx, rawQuery, qo, fieldMetadata.fieldMap)
	source, countQuery, _, err := buildESQuerySource(ctx, rawQuery, fact, nil)
	if err != nil {
		return nil, err
	}
	body, err := marshalSearchSource(source)
	if err != nil {
		return nil, err
	}

	return &PreparedRawQuery{
		query:         rawQuery,
		queryOption:   qo,
		fieldMetadata: fieldMetadata,
		fact:          fact,
		source:        source,
		countQuery:    countQuery,
		body:          body,
		connectionKey: i.RawBatchConnectionKey(ctx),
	}, nil
}

type rawFieldMetadataReuseProjection struct {
	Indexes    []string            `json:"indexes"`
	FieldAlias metadata.FieldAlias `json:"field_alias,omitempty"`
}

func rawFieldMetadataReuseIdentity(
	indexes []string,
	fieldAlias metadata.FieldAlias,
) ([sha256.Size]byte, error) {
	stableIndexes := append([]string(nil), indexes...)
	sort.Strings(stableIndexes)
	encoded, err := json.Marshal(rawFieldMetadataReuseProjection{
		Indexes:    stableIndexes,
		FieldAlias: fieldAlias,
	})
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(encoded), nil
}

func newRawFormatFactory(
	ctx context.Context,
	rawQuery *metadata.Query,
	qo *queryOption,
	fieldMap metadata.FieldsMap,
) *FormatFactory {
	unit := metadata.GetQueryParams(ctx).TimeUnit
	labelMap := function.LabelMap(ctx, rawQuery)
	reverseAlias := make(map[string]string, len(rawQuery.FieldAlias))
	for alias, original := range rawQuery.FieldAlias {
		reverseAlias[original] = alias
	}

	return NewFormatFactory(ctx).
		WithTransform(func(s string) string {
			if s == "" {
				return ""
			}
			if alias, ok := reverseAlias[s]; ok {
				return alias
			}
			return s
		}, func(s string) string {
			if s == "" {
				return ""
			}
			if original, ok := rawQuery.FieldAlias[s]; ok {
				return original
			}
			return s
		}).
		WithIsReference(metadata.GetQueryParams(ctx).IsReference).
		WithQuery(rawQuery.Field, rawQuery.TimeField, qo.start, qo.end, unit, rawQuery.Size).
		WithFieldMap(cloneFieldsMap(fieldMap)).
		WithOrders(append(metadata.Orders(nil), rawQuery.Orders...)).
		WithIncludeValues(labelMap)
}

func marshalSearchSource(source *elastic.SearchSource) (string, error) {
	if source == nil {
		return "", fmt.Errorf("empty es query source")
	}
	body, err := source.Source()
	if err != nil {
		return "", err
	}
	if body == nil {
		return "", fmt.Errorf("empty query body")
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func clonePreparedFieldMetadata(source *PreparedFieldMetadata) *PreparedFieldMetadata {
	if source == nil {
		return nil
	}
	return &PreparedFieldMetadata{
		indexes:         append([]string(nil), source.indexes...),
		physicalIndexes: append([]string(nil), source.physicalIndexes...),
		fieldMap:        cloneFieldsMap(source.fieldMap),
		connectionKey:   source.connectionKey,
		reuseIdentity:   source.reuseIdentity,
		complete:        source.complete,
	}
}

func cloneFieldsMap(source metadata.FieldsMap) metadata.FieldsMap {
	if source == nil {
		return nil
	}
	cloned := make(metadata.FieldsMap, len(source))
	for name, option := range source {
		option.TokenizeOnChars = append([]string(nil), option.TokenizeOnChars...)
		cloned[name] = option
	}
	return cloned
}

func cloneRawMetadataQuery(source *metadata.Query) *metadata.Query {
	if source == nil {
		return nil
	}

	cloned := *source
	cloned.TagsKey = append([]string(nil), source.TagsKey...)
	cloned.DBs = append([]string(nil), source.DBs...)
	cloned.Fields = append([]string(nil), source.Fields...)
	cloned.Measurements = append([]string(nil), source.Measurements...)
	cloned.MetricNames = append([]string(nil), source.MetricNames...)
	cloned.Aggregates = source.Aggregates.Copy()
	cloned.AllConditions = cloneAllConditions(source.AllConditions)
	cloned.Source = append([]string(nil), source.Source...)
	cloned.Orders = append(metadata.Orders(nil), source.Orders...)
	cloned.SelectDistinct = append([]string(nil), source.SelectDistinct...)
	cloned.ResultTableOption = source.ResultTableOption.Clone()

	if source.FieldAlias != nil {
		cloned.FieldAlias = make(metadata.FieldAlias, len(source.FieldAlias))
		for alias, original := range source.FieldAlias {
			cloned.FieldAlias[alias] = original
		}
	}
	if source.Filters != nil {
		cloned.Filters = make([]map[string]string, len(source.Filters))
		for index, filter := range source.Filters {
			if filter == nil {
				continue
			}
			cloned.Filters[index] = make(map[string]string, len(filter))
			for key, value := range filter {
				cloned.Filters[index][key] = value
			}
		}
	}
	if source.Collapse != nil {
		collapse := *source.Collapse
		cloned.Collapse = &collapse
	}

	return &cloned
}

func decodePreparedRawResult(
	prepared *PreparedRawQuery,
	sr *elastic.SearchResult,
	dataCh chan<- map[string]any,
) (size int64, total int64, option *metadata.ResultTableOption, err error) {
	rawQuery := prepared.query
	fact := prepared.fact
	from := rawQuery.From
	option = &metadata.ResultTableOption{
		FieldType: fact.FieldType(),
		From:      &from,
	}

	if sr == nil {
		return size, total, option, nil
	}
	if sr.Hits != nil {
		for idx, d := range sr.Hits.Hits {
			data := make(map[string]any)
			decoder := json.NewDecoder(bytes.NewReader(d.Source))
			decoder.UseNumber()
			if err = decoder.Decode(&data); err != nil {
				return size, total, option, err
			}
			fact.SetData(data)

			// 注入别名：命中原始字段 key 后补写别名字段 key（与 bksql 语义保持一致）
			rawQuery.FieldAlias.AddAliasKeysWhenOriginalFieldPresent(fact.data)

			fact.data[metadata.KeyDocID] = d.Id
			fact.data[metadata.KeyIndex] = d.Index
			rawQuery.DataReload(fact.data)

			if timeValue, ok := data[fact.GetTimeField().Name]; ok {
				fact.data[FieldTime] = timeValue
			}

			if idx == len(sr.Hits.Hits)-1 && d.Sort != nil {
				option.SearchAfter = d.Sort
			}

			dataCh <- fact.data
		}

		if sr.Hits.TotalHits != nil {
			total = sr.Hits.TotalHits.Value
		}
		size = int64(len(sr.Hits.Hits))
	}

	if rawQuery.Scroll != "" {
		originalOption := rawQuery.ResultTableOption
		option.ScrollID = sr.ScrollId

		if originalOption != nil {
			option.SliceIndex = originalOption.SliceIndex
			option.SliceMax = originalOption.SliceMax
		}
	}

	return size, total, option, nil
}
