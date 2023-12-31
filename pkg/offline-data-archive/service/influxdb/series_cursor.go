// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"context"

	"github.com/influxdata/influxdb/models"
	"github.com/influxdata/influxdb/query"
	"github.com/influxdata/influxdb/services/storage"
	"github.com/influxdata/influxdb/storage/reads"
	"github.com/influxdata/influxdb/storage/reads/datatypes"
	"github.com/influxdata/influxdb/tsdb"
	"github.com/influxdata/influxql"
)

var measurementRemap = map[string]string{
	measurementTagKey:        "_name",
	models.MeasurementTagKey: "_name",
	models.FieldKeyTagKey:    "_field",
}

var (
	measurementKeyBytes = []byte(measurementTagKey)
	fieldKeyBytes       = []byte(fieldTagKey)
)

type indexSeriesCursor struct {
	sqry            tsdb.SeriesCursor
	fields          measurementFields
	nf              []field
	field           field
	err             error
	tags            models.Tags
	cond            influxql.Expr
	measurementCond influxql.Expr
	row             reads.SeriesRow
	eof             bool
	hasFieldExpr    bool
	hasValueExpr    bool
}

func newIndexSeriesCursor(ctx context.Context, predicate *datatypes.Predicate, shards []*tsdb.Shard) (*indexSeriesCursor, error) {
	queries, err := tsdb.CreateCursorIterators(ctx, shards)
	if err != nil {
		return nil, err
	}

	if queries == nil {
		return nil, nil
	}

	opt := query.IteratorOptions{
		Aux:        []influxql.VarRef{{Val: "key"}},
		Authorizer: query.OpenAuthorizer,
		Ascending:  true,
		Ordered:    true,
	}
	p := &indexSeriesCursor{row: reads.SeriesRow{Query: queries}}

	if root := predicate.GetRoot(); root != nil {
		if p.cond, err = reads.NodeToExpr(root, measurementRemap); err != nil {
			return nil, err
		}

		p.hasFieldExpr, p.hasValueExpr = storage.HasFieldKeyOrValue(p.cond)
		if !(p.hasFieldExpr || p.hasValueExpr) {
			p.measurementCond = p.cond
			opt.Condition = p.cond
		} else {
			p.measurementCond = influxql.Reduce(reads.RewriteExprRemoveFieldValue(influxql.CloneExpr(p.cond)), nil)
			if reads.IsTrueBooleanLiteral(p.measurementCond) {
				p.measurementCond = nil
			}

			opt.Condition = influxql.Reduce(storage.RewriteExprRemoveFieldKeyAndValue(influxql.CloneExpr(p.cond)), nil)
			if reads.IsTrueBooleanLiteral(opt.Condition) {
				opt.Condition = nil
			}
		}
	}

	var mitr tsdb.MeasurementIterator
	name, singleMeasurement := storage.HasSingleMeasurementNoOR(p.measurementCond)
	if singleMeasurement {
		mitr = tsdb.NewMeasurementSliceIterator([][]byte{[]byte(name)})
	}

	sg := tsdb.Shards(shards)
	p.sqry, err = sg.CreateSeriesCursor(ctx, tsdb.SeriesCursorRequest{Measurements: mitr}, opt.Condition)
	if p.sqry != nil && err == nil {
		// Optimisation to check if request is only interested in results for a
		// single measurement. In this case we can efficiently produce all known
		// field keys from the collection of shards without having to go via
		// the query engine.
		if singleMeasurement {
			fkeys := sg.FieldKeysByMeasurement([]byte(name))
			if len(fkeys) == 0 {
				goto CLEANUP
			}

			fields := make([]field, 0, len(fkeys))
			for _, key := range fkeys {
				fields = append(fields, field{n: key, nb: []byte(key)})
			}
			p.fields = map[string][]field{name: fields}
			return p, nil
		}

		var mfkeys map[string][]string
		mfkeys, err = sg.FieldKeysByPredicate(opt.Condition)
		if err != nil {
			goto CLEANUP
		}

		p.fields = make(map[string][]field, len(mfkeys))
		for name, fkeys := range mfkeys {
			fields := make([]field, 0, len(fkeys))
			for _, key := range fkeys {
				fields = append(fields, field{n: key, nb: []byte(key)})
			}
			p.fields[name] = fields
		}
		return p, nil
	}

CLEANUP:
	p.Close()
	return nil, err
}

func (c *indexSeriesCursor) Close() {
	if !c.eof {
		c.eof = true
		if c.sqry != nil {
			c.sqry.Close()
			c.sqry = nil
		}
	}
}

func copyTags(dst, src models.Tags) models.Tags {
	if cap(dst) < src.Len() {
		dst = make(models.Tags, src.Len())
	} else {
		dst = dst[:src.Len()]
	}
	copy(dst, src)
	return dst
}

func (c *indexSeriesCursor) Next() *reads.SeriesRow {
	if c.eof {
		return nil
	}

	for {
		if len(c.nf) == 0 {
			// next series key
			sr, err := c.sqry.Next()
			if err != nil {
				c.err = err
				c.Close()
				return nil
			} else if sr == nil {
				c.Close()
				return nil
			}

			c.row.Name = sr.Name
			c.row.SeriesTags = sr.Tags
			c.tags = copyTags(c.tags, sr.Tags)
			c.tags.Set(measurementKeyBytes, sr.Name)

			c.nf = c.fields[string(sr.Name)]
			// c.nf may be nil if there are no fields
		} else {
			c.field, c.nf = c.nf[0], c.nf[1:]

			if c.measurementCond == nil || reads.EvalExprBool(c.measurementCond, c) {
				break
			}
		}
	}

	c.tags.Set(fieldKeyBytes, c.field.nb)
	c.row.Field = c.field.n

	if c.cond != nil && c.hasValueExpr {
		// TODO(sgc): lazily evaluate valueCond
		c.row.ValueCond = influxql.Reduce(c.cond, c)
		if reads.IsTrueBooleanLiteral(c.row.ValueCond) {
			// we've reduced the expression to "true"
			c.row.ValueCond = nil
		}
	}

	c.row.Tags = copyTags(c.row.Tags, c.tags)

	return &c.row
}

func (c *indexSeriesCursor) Value(key string) (interface{}, bool) {
	switch key {
	case "_name":
		return string(c.row.Name), true
	case fieldTagKey:
		return c.field.n, true
	case "$":
		return nil, false
	default:
		res := c.row.SeriesTags.GetString(key)
		return res, true
	}
}

func (c *indexSeriesCursor) Err() error {
	return c.err
}

type measurementFields map[string][]field

type field struct {
	n  string
	nb []byte
}
