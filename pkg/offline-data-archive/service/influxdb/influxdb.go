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
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gogo/protobuf/types"
	"github.com/influxdata/influxdb/models"
	"github.com/influxdata/influxdb/services/storage"
	"github.com/influxdata/influxdb/storage/reads"
	"github.com/influxdata/influxdb/storage/reads/datatypes"
	"github.com/influxdata/influxdb/tsdb"
	"github.com/influxdata/influxql"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/instance"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/policy/stores/shard"
	remoteRead "github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/service/influxdb/proto"
)

const (
	// fieldTagKey is the tag key that all field names use in the new storage processor
	fieldTagKey = "_field"

	// measurementTagKey is the tag key that all measurement names use in the new storage processor
	measurementTagKey = "_measurement"
)

type RawQuery struct {
	Ctx context.Context
	Log log.Logger

	WalDir  string
	DataDir string

	DB          string
	RP          string
	Measurement string
	Field       string
	Start       int64
	End         int64
	Condition   string
	Shards      []*shard.Shard

	EngineOptions tsdb.EngineOptions

	maxShardID uint64
	shards     tsdb.Shards
}

func (q *RawQuery) setShards() error {
	for _, sd := range q.Shards {
		q.maxShardID++
		uid := sd.Spec.Final.ShardID

		ins, err := instance.GetInstance(sd.Spec.Final.InstanceType)
		if err != nil {
			return err
		}

		shardPath := filepath.Join(q.DataDir, sd.Spec.Final.Path)

		// 增加判断逻辑，如果目录不存在才拷贝，提升查询速度
		_, err = os.Stat(shardPath)
		if errors.Is(err, os.ErrNotExist) {
			_, err := ins.Download(q.Ctx, sd.Spec.Final.Path, q.DataDir)
			if err != nil {
				return err
			}
		}

		walPath := filepath.Join(q.WalDir, q.DB, q.RP, uid)
		sFilePath := filepath.Join(shardPath, "_series")
		sFile := tsdb.NewSeriesFile(sFilePath)
		if err := sFile.Open(); err != nil {
			return err
		}

		q.EngineOptions.ShardID = q.maxShardID
		shard := tsdb.NewShard(q.maxShardID, shardPath, walPath, sFile, q.EngineOptions)
		if err := shard.Open(); err != nil {
			return err
		}
		q.shards = append(q.shards, shard)
	}
	return nil
}

func (q *RawQuery) ReadFilter() (reads.ResultSet, error) {
	var (
		cur *indexSeriesCursor
		rs  reads.ResultSet
		err error
	)

	// 加载 shards
	err = q.setShards()
	if err != nil {
		return rs, err
	}

	readRequest, err := getReadRequest(q.Ctx, q.DB, q.RP, q.Measurement, q.Field, q.Condition)
	if err != nil {
		return rs, err
	}
	cur, err = newIndexSeriesCursor(q.Ctx, readRequest.Predicate, q.shards)
	if err != nil {
		return rs, err
	}
	if cur != nil {
		rs = reads.NewFilteredResultSet(q.Ctx, readRequest, cur)
	}
	return rs, err
}

func (q *RawQuery) Close() error {
	for _, sd := range q.shards {
		sFile, err := sd.SeriesFile()
		if err != nil {
			q.Log.Errorf(q.Ctx, "series file get error: %s", err.Error())
			continue
		}
		err = sFile.Close()
		if err != nil {
			q.Log.Errorf(q.Ctx, "series file close error: %s", err.Error())
			continue
		}

		err = sd.Close()
		if err != nil {
			q.Log.Errorf(q.Ctx, "shard close error: %s", err.Error())
			continue
		}
	}
	return nil
}

func checkDir(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else {
		err := os.MkdirAll(path, 0711)
		if err != nil {
			return err
		}
	}
	return nil
}

// removeInfluxSystemTags will remove tags that are Influx internal (_measurement and _field)
func removeInfluxSystemTags(tags models.Tags) models.Tags {
	var t models.Tags
	for _, tt := range tags {
		if string(tt.Key) == measurementTagKey || string(tt.Key) == fieldTagKey {
			continue
		}
		t = append(t, tt)
	}

	return t
}

// modelTagsToLabelPairs converts models.Tags to a slice of Prometheus label pairs
func modelTagsToLabelPairs(tags models.Tags) []*remoteRead.LabelPair {
	pairs := make([]*remoteRead.LabelPair, 0, len(tags))
	for _, t := range tags {
		if string(t.Value) == "" {
			continue
		}
		pairs = append(pairs, &remoteRead.LabelPair{
			Name:  string(t.Key),
			Value: string(t.Value),
		})
	}
	return pairs
}

func getReadRequest(ctx context.Context, db, rp, measurement, field, where string) (*datatypes.ReadFilterRequest, error) {
	if db == "" {
		return nil, fmt.Errorf("db is empty")
	}
	if measurement == "" {
		return nil, fmt.Errorf("measurement is empty")
	}
	if field == "" {
		field = "value"
	}
	if where != "" {
		where = fmt.Sprintf("(%s) and ", where)
	}

	src, err := types.MarshalAny(&storage.ReadSource{Database: db, RetentionPolicy: rp})
	if err != nil {
		return nil, err
	}
	// 增加 measurement 和 field
	condition := fmt.Sprintf("%s%s = '%s' and %s = '%s'", where, measurementTagKey, measurement, fieldTagKey, field)
	expr, err := influxql.ParseExpr(condition)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	valuer := influxql.NowValuer{Now: now}
	cond, timeRange, err := influxql.ConditionExpr(expr, &valuer)
	if err != nil {
		return nil, err
	}

	predicate, err := exprToNode(cond)
	if err != nil {
		return nil, err
	}

	rq := &datatypes.ReadFilterRequest{
		ReadSource: src,
		Range: datatypes.TimestampRange{
			Start: timeRange.MinTimeNano(),
			End:   timeRange.MaxTimeNano(),
		},
		Predicate: predicate,
	}
	return rq, nil
}

func exprToNode(expr influxql.Expr) (*datatypes.Predicate, error) {
	if expr == nil {
		return nil, nil
	}
	var v exprToNodeVisitor
	influxql.Walk(&v, expr)
	if v.Err() != nil {
		return nil, v.Err()
	}

	return &datatypes.Predicate{Root: v.nodes[0]}, nil
}

type exprToNodeVisitor struct {
	nodes []*datatypes.Node
	err   error
}

func (v *exprToNodeVisitor) Err() error {
	return v.err
}

func (v *exprToNodeVisitor) pop() (top *datatypes.Node) {
	if len(v.nodes) < 1 {
		panic("exprToNodeVisitor: stack empty")
	}

	top, v.nodes = v.nodes[len(v.nodes)-1], v.nodes[:len(v.nodes)-1]
	return
}

func (v *exprToNodeVisitor) pop2() (lhs, rhs *datatypes.Node) {
	if len(v.nodes) < 2 {
		panic("exprToNodeVisitor: stack empty")
	}

	rhs = v.nodes[len(v.nodes)-1]
	lhs = v.nodes[len(v.nodes)-2]
	v.nodes = v.nodes[:len(v.nodes)-2]
	return
}

func (v *exprToNodeVisitor) mapOpToComparison(op influxql.Token) datatypes.Node_Comparison {
	switch op {
	case influxql.EQ:
		return datatypes.ComparisonEqual
	case influxql.EQREGEX:
		return datatypes.ComparisonRegex
	case influxql.NEQ:
		return datatypes.ComparisonNotEqual
	case influxql.NEQREGEX:
		return datatypes.ComparisonNotEqual
	case influxql.LT:
		return datatypes.ComparisonLess
	case influxql.LTE:
		return datatypes.ComparisonLessEqual
	case influxql.GT:
		return datatypes.ComparisonGreater
	case influxql.GTE:
		return datatypes.ComparisonGreaterEqual

	default:
		return -1
	}
}

func (v *exprToNodeVisitor) Visit(node influxql.Node) influxql.Visitor {
	switch n := node.(type) {
	case *influxql.BinaryExpr:
		if v.err != nil {
			return nil
		}

		influxql.Walk(v, n.LHS)
		if v.err != nil {
			return nil
		}

		influxql.Walk(v, n.RHS)
		if v.err != nil {
			return nil
		}

		if comp := v.mapOpToComparison(n.Op); comp != -1 {
			lhs, rhs := v.pop2()
			v.nodes = append(v.nodes, &datatypes.Node{
				NodeType: datatypes.NodeTypeComparisonExpression,
				Value:    &datatypes.Node_Comparison_{Comparison: comp},
				Children: []*datatypes.Node{lhs, rhs},
			})
		} else if n.Op == influxql.AND || n.Op == influxql.OR {
			var op datatypes.Node_Logical
			if n.Op == influxql.AND {
				op = datatypes.LogicalAnd
			} else {
				op = datatypes.LogicalOr
			}

			lhs, rhs := v.pop2()
			v.nodes = append(v.nodes, &datatypes.Node{
				NodeType: datatypes.NodeTypeLogicalExpression,
				Value:    &datatypes.Node_Logical_{Logical: op},
				Children: []*datatypes.Node{lhs, rhs},
			})
		} else {
			v.err = fmt.Errorf("unsupported operator, %s", n.Op)
		}

		return nil

	case *influxql.ParenExpr:
		influxql.Walk(v, n.Expr)
		if v.err != nil {
			return nil
		}

		v.nodes = append(v.nodes, &datatypes.Node{
			NodeType: datatypes.NodeTypeParenExpression,
			Children: []*datatypes.Node{v.pop()},
		})
		return nil

	case *influxql.StringLiteral:
		v.nodes = append(v.nodes, &datatypes.Node{
			NodeType: datatypes.NodeTypeLiteral,
			Value:    &datatypes.Node_StringValue{StringValue: n.Val},
		})
		return nil

	case *influxql.NumberLiteral:
		v.nodes = append(v.nodes, &datatypes.Node{
			NodeType: datatypes.NodeTypeLiteral,
			Value:    &datatypes.Node_FloatValue{FloatValue: n.Val},
		})
		return nil

	case *influxql.IntegerLiteral:
		v.nodes = append(v.nodes, &datatypes.Node{
			NodeType: datatypes.NodeTypeLiteral,
			Value:    &datatypes.Node_IntegerValue{IntegerValue: n.Val},
		})
		return nil

	case *influxql.UnsignedLiteral:
		v.nodes = append(v.nodes, &datatypes.Node{
			NodeType: datatypes.NodeTypeLiteral,
			Value:    &datatypes.Node_UnsignedValue{UnsignedValue: n.Val},
		})
		return nil

	case *influxql.VarRef:
		v.nodes = append(v.nodes, &datatypes.Node{
			NodeType: datatypes.NodeTypeTagRef,
			Value:    &datatypes.Node_TagRefValue{TagRefValue: n.Val},
		})
		return nil

	case *influxql.RegexLiteral:
		v.nodes = append(v.nodes, &datatypes.Node{
			NodeType: datatypes.NodeTypeLiteral,
			Value:    &datatypes.Node_RegexValue{RegexValue: n.Val.String()},
		})
		return nil
	default:
		v.err = fmt.Errorf("unsupported expression %T", n)
		return nil
	}
}
