// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"encoding/json"
	"errors"
	"hash/maphash"
	"io"
	"math"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

const (
	sharedSchemaMaxFrameBytes = 256 * 1024 // Includes the terminating LF.
	sharedSchemaMaxSeries     = 256
	sharedSchemaMaxSchemas    = 1024
	sharedSchemaMaxDictBytes  = 8 * 1024 * 1024

	sharedFallbackShape      = "invalid_shape"
	sharedFallbackScalar     = "unsupported_scalar"
	sharedFallbackFrame      = "frame_limit"
	sharedFallbackSchemas    = "schema_limit"
	sharedFallbackDictionary = "dictionary_limit"
	sharedFailureEncode      = "encode"
)

type sharedSchema struct {
	ID        int      `json:"id"`
	Columns   []string `json:"columns"`
	Types     []string `json:"types"`
	GroupKeys []string `json:"group_keys"`
}

type sharedSeries struct {
	SchemaID int      `json:"s"`
	Groups   []string `json:"g"`
	Rows     [][]any  `json:"r"`
}

type sharedEnd struct {
	Series        int64            `json:"series"`
	Points        int64            `json:"points"`
	IsPartial     bool             `json:"is_partial"`
	Status        *metadata.Status `json:"status,omitempty"`
	ResultTableID *[]string        `json:"result_table_id,omitempty"`
	TraceID       string           `json:"trace_id,omitempty"`
}

type sharedFrame struct {
	Seq     int            `json:"seq"`
	Version int            `json:"v,omitempty"`
	Schemas []sharedSchema `json:"schemas,omitempty"`
	Series  []sharedSeries `json:"series,omitempty"`
	End     *sharedEnd     `json:"end,omitempty"`
}

// The plan retains only a bounded schema registry and footer. It never keeps
// an encoded body, or per-series buffers/lengths proportional to result size.
// All references point to the query's completed, owned PromData.
type sharedSchemaPlan struct {
	schemas []sharedSchema
	seed    maphash.Seed
	index   map[uint64][]int
	end     sharedEnd
}

type sharedSchemaWriteStats struct {
	Bytes          int64
	Frames         int
	EncodeDuration time.Duration
	WriteDuration  time.Duration
	FailureStage   string
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func seriesProjection(table *TablesItem, id int) sharedSeries {
	rows := table.Values
	if rows == nil {
		rows = [][]any{}
	}
	return sharedSeries{SchemaID: id, Groups: nonNilStrings(table.GroupValues), Rows: rows}
}

func (p *sharedSchemaPlan) schemaHash(table *TablesItem) uint64 {
	var hash maphash.Hash
	hash.SetSeed(p.seed)
	for _, values := range [][]string{table.Columns, table.Types, table.GroupKeys} {
		hash.WriteString(strconv.Itoa(len(values)))
		hash.WriteByte(':')
		for _, value := range values {
			hash.WriteString(strconv.Itoa(len(value)))
			hash.WriteByte(':')
			hash.WriteString(value)
		}
	}
	return hash.Sum64()
}

func (p *sharedSchemaPlan) schemaID(table *TablesItem, hash uint64) (int, bool) {
	for _, id := range p.index[hash] {
		schema := p.schemas[id]
		if slices.Equal(schema.Columns, table.Columns) && slices.Equal(schema.Types, table.Types) && slices.Equal(schema.GroupKeys, table.GroupKeys) {
			return id, true
		}
	}
	return 0, false
}

// preflightSharedSchema completes before any header or body is committed.
// Count each scalar and structural delimiter, stopping at the frame limit;
// do not Marshal a candidate object to discover whether it is too large.
func preflightSharedSchema(ctx context.Context, data *PromData) (*sharedSchemaPlan, string, error) {
	plan := &sharedSchemaPlan{seed: maphash.MakeSeed(), index: make(map[uint64][]int)}
	if data == nil {
		return nil, sharedFallbackShape, nil
	}
	dictionaryBytes := 0
	for _, table := range data.Tables {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		if table == nil || len(table.Columns) != len(table.Types) || len(table.GroupKeys) != len(table.GroupValues) {
			return nil, sharedFallbackShape, nil
		}
		// Even a duplicate schema is checked before hashing potentially huge
		// strings. The counter itself has fixed scratch space.
		candidate := sharedSchema{ID: len(plan.schemas), Columns: nonNilStrings(table.Columns), Types: nonNilStrings(table.Types), GroupKeys: nonNilStrings(table.GroupKeys)}
		counter := newSharedSizeCounter(ctx)
		counter.schema(candidate)
		if reason, err := counter.result(); reason != "" || err != nil {
			return nil, reason, err
		}
		hash := plan.schemaHash(table)
		if _, exists := plan.schemaID(table, hash); exists {
			continue
		}
		if len(plan.schemas) == sharedSchemaMaxSchemas {
			return nil, sharedFallbackSchemas, nil
		}
		if counter.size+schemaFrameOverhead(len(plan.schemas)+1) > sharedSchemaMaxFrameBytes {
			return nil, sharedFallbackFrame, nil
		}
		dictionaryBytes += counter.size
		if dictionaryBytes > sharedSchemaMaxDictBytes {
			return nil, sharedFallbackDictionary, nil
		}
		plan.index[hash] = append(plan.index[hash], candidate.ID)
		plan.schemas = append(plan.schemas, candidate)
	}

	seq, batchSize, batchCount := len(plan.schemas)+1, 0, 0
	for _, table := range data.Tables {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		id, _ := plan.schemaID(table, plan.schemaHash(table))
		counter := newSharedSizeCounter(ctx)
		counter.series(seriesProjection(table, id), len(table.Columns))
		if reason, err := counter.result(); reason != "" || err != nil {
			return nil, reason, err
		}
		if batchCount > 0 && (batchCount == sharedSchemaMaxSeries || dataFrameOverhead(seq)+batchSize+1+counter.size > sharedSchemaMaxFrameBytes) {
			seq++
			batchSize, batchCount = 0, 0
		}
		if dataFrameOverhead(seq)+counter.size > sharedSchemaMaxFrameBytes {
			return nil, sharedFallbackFrame, nil
		}
		if batchCount > 0 {
			batchSize++ // Comma between series.
		}
		batchSize += counter.size
		batchCount++
		plan.end.Series++
		plan.end.Points += int64(len(table.Values))
	}
	if batchCount > 0 {
		seq++
	}
	plan.end.IsPartial, plan.end.Status, plan.end.TraceID = data.IsPartial, data.Status, data.TraceID
	if data.includeResultTableID || len(data.ResultTableID) > 0 {
		ids := nonNilStrings(data.ResultTableID)
		plan.end.ResultTableID = &ids
	}
	counter := newSharedSizeCounter(ctx)
	counter.add(len(`{"seq":`) + decimalSize(seq) + len(`,"end":`) + len("}\n"))
	counter.end(plan.end)
	if reason, err := counter.result(); reason != "" || err != nil {
		return nil, reason, err
	}
	return plan, "", nil
}

func decimalSize(value int) int { return len(strconv.Itoa(value)) }

func schemaFrameOverhead(seq int) int {
	return len(`{"seq":`) + decimalSize(seq) + len(`,"schemas":[`) + len("]}\n")
}

func dataFrameOverhead(seq int) int {
	return len(`{"seq":`) + decimalSize(seq) + len(`,"series":[`) + len("]}\n")
}

// writeSharedSchema uses synchronous writes and at most one bounded encoded
// frame. No queue is placed between encoding and HTTP backpressure. Call only
// with an unchanged PromData that passed preflightSharedSchema.
func writeSharedSchema(ctx context.Context, writer io.Writer, flush func() error, data *PromData, plan *sharedSchemaPlan) (sharedSchemaWriteStats, error) {
	var stats sharedSchemaWriteStats
	seq := 0
	writeFrame := func(frame sharedFrame) error {
		if err := ctx.Err(); err != nil {
			stats.FailureStage = "stream_cancel"
			return err
		}
		frame.Seq = seq
		started := time.Now()
		body, err := json.Marshal(frame)
		stats.EncodeDuration += time.Since(started)
		if err != nil {
			stats.FailureStage = sharedFailureEncode
			return err
		}
		// A mismatch is an encoder bug, not a reason to fall back mid-stream.
		if len(body)+1 > sharedSchemaMaxFrameBytes {
			stats.FailureStage = sharedFailureEncode
			return errors.New("shared schema frame exceeded preflight size")
		}
		body = append(body, '\n')
		started = time.Now()
		n, err := writer.Write(body)
		stats.Bytes += int64(n)
		if err == nil && n != len(body) {
			err = io.ErrShortWrite
		}
		if err == nil && flush != nil {
			err = flush()
		}
		stats.WriteDuration += time.Since(started)
		if err != nil {
			stats.FailureStage = "write"
			return err
		}
		stats.Frames++
		seq++
		return nil
	}
	if err := writeFrame(sharedFrame{Version: 1}); err != nil {
		return stats, err
	}
	for _, schema := range plan.schemas {
		if err := writeFrame(sharedFrame{Schemas: []sharedSchema{schema}}); err != nil {
			return stats, err
		}
	}
	batch := make([]sharedSeries, 0, sharedSchemaMaxSeries)
	batchSize := 0
	for _, table := range data.Tables {
		if err := ctx.Err(); err != nil {
			stats.FailureStage = "stream_cancel"
			return stats, err
		}
		started := time.Now()
		id, _ := plan.schemaID(table, plan.schemaHash(table))
		series := seriesProjection(table, id)
		counter := newSharedSizeCounter(ctx)
		counter.series(series, len(table.Columns))
		stats.EncodeDuration += time.Since(started)
		if reason, err := counter.result(); reason != "" || err != nil {
			stats.FailureStage = sharedFailureEncode
			if err != nil {
				return stats, err
			}
			return stats, errors.New("shared schema result changed after preflight")
		}
		if len(batch) > 0 && (len(batch) == sharedSchemaMaxSeries || dataFrameOverhead(seq)+batchSize+1+counter.size > sharedSchemaMaxFrameBytes) {
			if err := writeFrame(sharedFrame{Series: batch}); err != nil {
				return stats, err
			}
			clear(batch)
			batch, batchSize = batch[:0], 0
		}
		if len(batch) > 0 {
			batchSize++
		}
		batch = append(batch, series)
		batchSize += counter.size
	}
	if len(batch) > 0 {
		if err := writeFrame(sharedFrame{Series: batch}); err != nil {
			return stats, err
		}
	}
	if err := writeFrame(sharedFrame{End: &plan.end}); err != nil {
		return stats, err
	}
	return stats, nil
}

// This counter matches encoding/json's default escaping and primitive scalar
// formatting. It allocates no scratch proportional to a string/row/object.
// Named scalar types, pointers and custom Marshalers use the legacy outlet.
type sharedSizeCounter struct {
	ctx    context.Context
	size   int
	reason string
	err    error
}

func newSharedSizeCounter(ctx context.Context) *sharedSizeCounter {
	return &sharedSizeCounter{ctx: ctx}
}

func (c *sharedSizeCounter) result() (string, error) { return c.reason, c.err }

func (c *sharedSizeCounter) add(size int) {
	if c.reason != "" || c.err != nil {
		return
	}
	if size > sharedSchemaMaxFrameBytes-c.size {
		c.reason = sharedFallbackFrame
		return
	}
	c.size += size
}

func (c *sharedSizeCounter) active() bool {
	if c.reason != "" || c.err != nil {
		return false
	}
	c.err = c.ctx.Err()
	return c.err == nil
}

func (c *sharedSizeCounter) string(value string) {
	c.add(2)
	for i := 0; i < len(value); {
		if c.reason != "" || c.err != nil || i%4096 == 0 && !c.active() {
			return
		}
		b := value[i]
		if b < utf8.RuneSelf {
			size := 1
			switch b {
			case '\\', '"', '\n', '\r', '\t', '\b', '\f':
				size = 2
			case '<', '>', '&':
				size = 6
			default:
				if b < 0x20 {
					size = 6
				}
			}
			c.add(size)
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(value[i:])
		if r == utf8.RuneError && size == 1 || r == '\u2028' || r == '\u2029' {
			c.add(6)
		} else {
			c.add(size)
		}
		i += size
	}
}

func (c *sharedSizeCounter) strings(values []string) {
	c.add(2)
	for i, value := range values {
		if !c.active() {
			return
		}
		if i > 0 {
			c.add(1)
		}
		c.string(value)
	}
}

func (c *sharedSizeCounter) schema(schema sharedSchema) {
	c.add(len(`{"id":`) + decimalSize(schema.ID) + len(`,"columns":`))
	c.strings(schema.Columns)
	c.add(len(`,"types":`))
	c.strings(schema.Types)
	c.add(len(`,"group_keys":`))
	c.strings(schema.GroupKeys)
	c.add(1)
}

func (c *sharedSizeCounter) series(series sharedSeries, width int) {
	c.add(len(`{"s":`) + decimalSize(series.SchemaID) + len(`,"g":`))
	c.strings(series.Groups)
	c.add(len(`,"r":[`))
	for i, row := range series.Rows {
		if !c.active() {
			return
		}
		if len(row) != width {
			c.reason = sharedFallbackShape
			return
		}
		// v1 rows must be arrays. A nil legacy row stays on the old outlet.
		if i > 0 {
			c.add(1)
		}
		if row == nil {
			c.reason = sharedFallbackShape
			return
		}
		c.add(2)
		for j, scalar := range row {
			if !c.active() {
				return
			}
			if j > 0 {
				c.add(1)
			}
			c.scalar(scalar)
		}
	}
	c.add(2)
}

func (c *sharedSizeCounter) scalar(value any) {
	var scratch [32]byte
	var body []byte
	switch value := value.(type) {
	case nil:
		c.add(4)
		return
	case string:
		c.string(value)
		return
	case bool:
		if value {
			c.add(4)
		} else {
			c.add(5)
		}
		return
	case int:
		body = strconv.AppendInt(scratch[:0], int64(value), 10)
	case int8:
		body = strconv.AppendInt(scratch[:0], int64(value), 10)
	case int16:
		body = strconv.AppendInt(scratch[:0], int64(value), 10)
	case int32:
		body = strconv.AppendInt(scratch[:0], int64(value), 10)
	case int64:
		body = strconv.AppendInt(scratch[:0], value, 10)
	case uint:
		body = strconv.AppendUint(scratch[:0], uint64(value), 10)
	case uint8:
		body = strconv.AppendUint(scratch[:0], uint64(value), 10)
	case uint16:
		body = strconv.AppendUint(scratch[:0], uint64(value), 10)
	case uint32:
		body = strconv.AppendUint(scratch[:0], uint64(value), 10)
	case uint64:
		body = strconv.AppendUint(scratch[:0], value, 10)
	case float32:
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			c.reason = sharedFallbackScalar
			return
		}
		body = appendSharedFloat(scratch[:0], float64(value), 32)
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			c.reason = sharedFallbackScalar
			return
		}
		body = appendSharedFloat(scratch[:0], value, 64)
	case json.Number:
		if len(value) > sharedSchemaMaxFrameBytes-c.size {
			c.reason = sharedFallbackFrame
			return
		}
		if !validSharedNumber(string(value)) {
			c.reason = sharedFallbackScalar
			return
		}
		if value == "" {
			c.add(1) // encoding/json encodes an empty Number as 0.
		} else {
			c.add(len(value))
		}
		return
	default:
		c.reason = sharedFallbackScalar
		return
	}
	c.add(len(body))
}

// Mirrors encoding/json's ES6-compatible float formatting. Scientific
// notation also removes the redundant zero in a negative exponent.
func appendSharedFloat(dst []byte, value float64, bits int) []byte {
	abs := math.Abs(value)
	format := byte('f')
	if bits == 64 && abs != 0 && (abs < 1e-6 || abs >= 1e21) || bits == 32 && abs != 0 && (float32(abs) < 1e-6 || float32(abs) >= 1e21) {
		format = 'e'
	}
	dst = strconv.AppendFloat(dst, value, format, -1, bits)
	if format == 'e' {
		n := len(dst)
		if n >= 4 && dst[n-4] == 'e' && dst[n-3] == '-' && dst[n-2] == '0' {
			dst[n-2] = dst[n-1]
			dst = dst[:n-1]
		}
	}
	return dst
}

// Validate JSON number syntax without copying a possibly large Number.
func validSharedNumber(value string) bool {
	if value == "" {
		return true
	}
	i := 0
	if value[i] == '-' {
		i++
	}
	if i == len(value) {
		return false
	}
	if value[i] == '0' {
		i++
	} else {
		if value[i] < '1' || value[i] > '9' {
			return false
		}
		for i < len(value) && value[i] >= '0' && value[i] <= '9' {
			i++
		}
	}
	if i < len(value) && value[i] == '.' {
		i++
		start := i
		for i < len(value) && value[i] >= '0' && value[i] <= '9' {
			i++
		}
		if i == start {
			return false
		}
	}
	if i < len(value) && (value[i] == 'e' || value[i] == 'E') {
		i++
		if i < len(value) && (value[i] == '+' || value[i] == '-') {
			i++
		}
		start := i
		for i < len(value) && value[i] >= '0' && value[i] <= '9' {
			i++
		}
		if i == start {
			return false
		}
	}
	return i == len(value)
}

func (c *sharedSizeCounter) end(end sharedEnd) {
	c.add(len(`{"series":`) + len(strconv.FormatInt(end.Series, 10)) + len(`,"points":`) + len(strconv.FormatInt(end.Points, 10)) + len(`,"is_partial":`))
	c.scalar(end.IsPartial)
	if end.Status != nil {
		c.add(len(`,"status":{"code":`))
		c.string(end.Status.Code)
		c.add(len(`,"message":`))
		c.string(end.Status.Message)
		c.add(1)
	}
	if end.ResultTableID != nil {
		c.add(len(`,"result_table_id":`))
		c.strings(*end.ResultTableID)
	}
	if end.TraceID != "" {
		c.add(len(`,"trace_id":`))
		c.string(end.TraceID)
	}
	c.add(1)
}
