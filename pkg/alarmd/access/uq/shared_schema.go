package uq

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

const (
	SharedSchemaMediaType = "application/vnd.bkmonitor.uq.shared-schema.v1+ndjson"
	sharedSchemaAccept    = SharedSchemaMediaType + ", application/json;q=0.9"
	sharedFrameBytes      = 256 << 10 // A complete line, including LF.
	sharedFrameSeries     = 256
	sharedSchemaCount     = 1024
	sharedDictionaryBytes = 8 << 20 // Sum of encoded schema declaration objects.
)

var (
	errFrameBytes  = &responseLimitError{code: "SHARED_SCHEMA_FRAME_BYTES_EXCEEDED", class: execution.ResponseFailureLimitFrameBytes}
	errFrameSeries = &responseLimitError{code: "SHARED_SCHEMA_FRAME_SERIES_EXCEEDED", class: execution.ResponseFailureLimitFrameSeries}
	errDictionary  = &responseLimitError{code: "SHARED_SCHEMA_DICTIONARY_EXCEEDED", class: execution.ResponseFailureLimitDictionary}
)

// sharedFrameReaders holds the readers a shared-schema response is read
// through, each with a buffer of one whole frame. A reader of its own per
// response was 256 KiB allocated for every response, about 1.3 KB per
// admitted decision fleet-wide. A reader goes back after its response and
// is reset onto the next one's body, dropping whatever the last left
// unread; nothing decoded keeps a slice of its buffer - every frame is
// copied out by wireObject before the next read - so the next response may
// read through it.
var sharedFrameReaders = sync.Pool{New: func() any { return bufio.NewReaderSize(nil, sharedFrameBytes) }}

// Protocol failures never include provider payloads in their error text.
type sharedProtocolError struct{ class string }

func (e *sharedProtocolError) Error() string { return "alarmd access uq: shared schema " + e.class }
func (e *sharedProtocolError) QueryFailure() (string, string) {
	return "source_backend", "SHARED_SCHEMA_" + strings.ToUpper(e.class)
}
func (e *sharedProtocolError) QueryFailureDetail() string {
	return execution.ResponseRouteDetail(e.class)
}

func protocolError(class string) error { return &sharedProtocolError{class: class} }

func wireUnsigned(raw json.RawMessage, value *uint64) bool {
	if len(raw) == 0 || raw[0] < '0' || raw[0] > '9' || len(raw) > 1 && raw[0] == '0' {
		return false
	}
	parsed, err := strconv.ParseUint(string(raw), 10, 64)
	if err != nil {
		return false
	}
	*value = parsed
	return true
}

type sharedSchema struct {
	ID        uint32   `json:"id"`
	Columns   []string `json:"columns"`
	Types     []string `json:"types"`
	GroupKeys []string `json:"group_keys"`
}

// selectSharedDecoder leaves an unselected client's legacy Content-Type
// tolerance intact. An unsolicited shared response is always rejected.
func selectSharedDecoder(contentType string, optedIn bool) (bool, error) {
	media, _, err := mime.ParseMediaType(contentType)
	if contentType == "" {
		return false, nil
	}
	if err == nil && media == SharedSchemaMediaType {
		if !optedIn {
			return false, protocolError(execution.ResponseFailureFormatUnsupported)
		}
		return true, nil
	}
	if err == nil && strings.HasPrefix(media, "application/vnd.bkmonitor.uq.shared-schema.") {
		return false, protocolError(execution.ResponseFailureFormatUnsupported)
	}
	if !optedIn || err == nil && media == "application/json" {
		return false, nil
	}
	return false, protocolError(execution.ResponseFailureFormatUnsupported)
}

// wireObject rejects duplicate fields, non-objects and trailing JSON. Unknown
// fields remain forward-compatible and are included in the reader's budget.
func wireObject(raw []byte) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return nil, protocolError(execution.ResponseFailureFrameInvalid)
	}
	fields := make(map[string]json.RawMessage)
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return nil, protocolError(execution.ResponseFailureFrameInvalid)
		}
		name, ok := key.(string)
		if !ok {
			return nil, protocolError(execution.ResponseFailureFrameInvalid)
		}
		if _, duplicate := fields[name]; duplicate {
			return nil, protocolError(execution.ResponseFailureFrameInvalid)
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, protocolError(execution.ResponseFailureFrameInvalid)
		}
		fields[name] = value
	}
	if _, err := d.Token(); err != nil {
		return nil, protocolError(execution.ResponseFailureFrameInvalid)
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, protocolError(execution.ResponseFailureFrameInvalid)
	}
	return fields, nil
}

func (client *Client) decodeShared(
	ctx context.Context, abort func(), reader io.Reader, attempt queryIdentity, sink execution.ProviderSeriesSink,
) (completion execution.ProviderCompletion, err error) {
	session := client.newSeriesDecoder(attempt, sink, nil)
	// A frame is read whole before its series are handed on, so they may be
	// made canonical side by side (seriesDecoder.handOn).
	session.framed = true
	defer session.limitCompletion(&completion, &err)
	defer session.settle(ctx, &err)
	// The frames are read and parsed on a goroutine of their own, one frame
	// ahead of the one being handed on (readSharedFrames): reading the
	// socket and parsing a frame was the Slot's path while the frame before
	// was evaluated, and the evaluation waited on it in turn (decision-020
	// 0.6.x throughput design 3.1, way 2). Everything the answer's series
	// are counted and judged by - its limits, its footer's counts - is
	// still here, in the order the frames came.
	frames := make(chan sharedFrame, 1)
	stop, finished := make(chan struct{}), make(chan struct{})
	// readerEnded is set when the reader ended the answer itself - the end
	// of the body, or a frame that failed - and returns on its own.
	readerEnded := false
	// Returned with the reader stopped, whatever ended the answer: nothing
	// reads the body once this returns, so the caller's reading of it - its
	// bytes, its failure - is of reads that are over. Ended here instead, by
	// a series or the footer this side refused, the reader may be in a read
	// of the body, which closing it ends (abort).
	defer func() {
		close(stop)
		if !readerEnded && abort != nil {
			select {
			case <-finished:
			default:
				abort()
			}
		}
		<-finished
	}()
	go func() {
		defer close(finished)
		client.readSharedFrames(ctx, reader, frames, stop)
	}()
	var footer map[string]json.RawMessage
	for frame := range frames {
		readerEnded = frame.err != nil
		switch {
		case frame.version:
			session.receivedAt = frame.receivedAt
		case frame.footer != nil:
			footer = frame.footer
			var count, points uint64
			if !wireUnsigned(footer["series"], &count) || !wireUnsigned(footer["points"], &points) || count != session.totalSeries || points != session.totalRecords {
				return completion, protocolError(execution.ResponseFailureFooterCountMismatch)
			}
		}
		for _, series := range frame.series {
			if err := session.accept(ctx, series.series, series.size); err != nil {
				return completion, err
			}
		}
		if frame.err != nil {
			// The series parsed before it are handed on (settle), as each
			// accepted before a failure was.
			return completion, frame.err
		}
		if len(frame.series) > 0 {
			// The frame's series are handed on before the next is taken: a
			// series that came is delivered whether or not more come after
			// it.
			if err := session.handOn(ctx); err != nil {
				return completion, err
			}
		}
	}
	// Closed: the reader read to the body's end.
	readerEnded = true
	if footer == nil {
		// The reader ends on an error or after a footer and the end of the
		// answer; it never closes without one of the two.
		return completion, protocolError(execution.ResponseFailureFooterMissing)
	}
	var status *responseStatus
	var partial *bool
	var tables []string
	if raw, exists := footer["is_partial"]; exists {
		var value bool
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil {
			return completion, protocolError(execution.ResponseFailureFrameInvalid)
		}
		partial = &value
	}
	if raw := footer["status"]; raw != nil && json.Unmarshal(raw, &status) != nil {
		return completion, protocolError(execution.ResponseFailureFrameInvalid)
	}
	if raw := footer["result_table_id"]; raw != nil && json.Unmarshal(raw, &tables) != nil {
		return completion, protocolError(execution.ResponseFailureFrameInvalid)
	}
	// Trace is diagnostic metadata, never a record/delivery identity. Like the
	// legacy decoder, this provider does not project it into business facts.
	if raw := footer["trace_id"]; raw != nil {
		var trace string
		if json.Unmarshal(raw, &trace) != nil {
			return completion, protocolError(execution.ResponseFailureFrameInvalid)
		}
	}
	return session.finish(ctx, status, partial, tables)
}

// expandedSeriesBytes counts the consumer DTO without marshalling it. Raw
// scalar whitespace is retained and HTML/separator escaping is charged
// conservatively, so this never undercounts json.Marshal(responseSeries).
func expandedSeriesBytes(series responseSeries, maximum uint64) (uint64, error) {
	size := uint64(len(`{"name":,"columns":,"types":,"group_keys":,"group_values":,"values":}`))
	add := func(n uint64) error {
		if size > maximum || n > maximum-size {
			return ErrSeriesBytesExceeded
		}
		size += n
		return nil
	}
	if err := add(jsonStringBytes(series.Name)); err != nil {
		return 0, err
	}
	for _, values := range [][]string{series.Columns, series.Types, series.GroupKeys} {
		if values == nil {
			if err := add(4); err != nil {
				return 0, err
			}
			continue
		}
		if err := add(2); err != nil {
			return 0, err
		}
		for i, value := range values {
			if i != 0 {
				if err := add(1); err != nil {
					return 0, err
				}
			}
			if err := add(jsonStringBytes(value)); err != nil {
				return 0, err
			}
		}
	}
	countRaw := func(values []json.RawMessage) error {
		if values == nil {
			return add(4)
		}
		if err := add(2); err != nil {
			return err
		}
		for i, raw := range values {
			if i != 0 {
				if err := add(1); err != nil {
					return err
				}
			}
			if len(raw) == 0 {
				if err := add(4); err != nil {
					return err
				}
				continue
			}
			if err := add(uint64(len(raw))); err != nil {
				return err
			}
			for _, r := range string(raw) {
				switch r {
				case '<', '>', '&':
					if err := add(5); err != nil {
						return err
					}
				case '\u2028', '\u2029':
					if err := add(3); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	if err := countRaw(series.GroupValues); err != nil {
		return 0, err
	}
	if series.Values == nil {
		if err := add(4); err != nil {
			return 0, err
		}
	} else {
		if err := add(2); err != nil {
			return 0, err
		}
		for i, row := range series.Values {
			if i != 0 {
				if err := add(1); err != nil {
					return 0, err
				}
			}
			if err := countRaw(row); err != nil {
				return 0, err
			}
		}
	}
	if size > maximum {
		return 0, ErrSeriesBytesExceeded
	}
	return size, nil
}

func jsonStringBytes(value string) uint64 {
	size := uint64(2)
	for len(value) > 0 {
		c := value[0]
		if c < utf8.RuneSelf {
			value = value[1:]
			switch c {
			case '"', '\\', '\n', '\r', '\t', '\b', '\f':
				size += 2
			case '<', '>', '&':
				size += 6
			default:
				if c < 0x20 {
					size += 6
				} else {
					size++
				}
			}
			continue
		}
		r, n := utf8.DecodeRuneInString(value)
		value = value[n:]
		if r == utf8.RuneError && n == 1 || r == '\u2028' || r == '\u2029' {
			size += 6
		} else {
			size += uint64(n)
		}
	}
	return size
}

// sharedFrame is one frame of a shared-schema answer as readSharedFrames
// parsed it: the version frame (with when it came), a frame of series - each
// with its expanded size - or the footer; err is the failure that ended the
// answer, with the series of its frame parsed before it.
type sharedFrame struct {
	version    bool
	receivedAt int64
	series     []sharedSeries
	footer     map[string]json.RawMessage
	err        error
}

type sharedSeries struct {
	series responseSeries
	size   uint64
}

// readSharedFrames reads and parses an answer's frames in order onto frames,
// at most one ahead of the one being handed on, and closes it after the
// footer and the end of the answer, or after the frame that failed. It
// stops when stop closes; a read it is in returns when the answer's body is
// closed, which decodeShared does before it returns when the answer ended
// on its side (abort). The frame reader goes back to its pool from here, the
// one goroutine that reads through it.
//
// What it holds ahead is one frame parsed - sharedFrameSeries series of a
// line of sharedFrameBytes at most - beside the one being handed on: bounded
// by the frame's own limits, and not charged to the share, as the frame being
// read was not.
func (client *Client) readSharedFrames(ctx context.Context, reader io.Reader, frames chan<- sharedFrame, stop <-chan struct{}) {
	defer close(frames)
	lines := sharedFrameReaders.Get().(*bufio.Reader)
	lines.Reset(reader)
	defer sharedFrameReaders.Put(lines)
	send := func(frame sharedFrame) bool {
		select {
		case frames <- frame:
			return frame.err == nil
		case <-stop:
			return false
		}
	}
	fail := func(err error) { send(sharedFrame{err: err}) }
	var schemas []sharedSchema
	var dictionaryBytes uint64
	var expected uint64
	footer := false
	for {
		if err := ctx.Err(); err != nil {
			fail(err)
			return
		}
		line, err := lines.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			fail(errFrameBytes)
			return
		}
		if err == io.EOF {
			if len(line) != 0 {
				fail(protocolError(execution.ResponseFailureFrameInvalid))
				return
			}
			if !footer {
				fail(protocolError(execution.ResponseFailureFooterMissing))
			}
			return
		}
		if err != nil {
			fail(err)
			return
		}
		if footer || !utf8.Valid(line) {
			fail(protocolError(execution.ResponseFailureFrameInvalid))
			return
		}
		frame, err := wireObject(line)
		if err != nil {
			fail(err)
			return
		}
		var seq uint64
		if !wireUnsigned(frame["seq"], &seq) || seq != expected {
			fail(protocolError(execution.ResponseFailureFrameInvalid))
			return
		}
		expected++
		payloads := 0
		for _, key := range []string{"v", "schemas", "series", "end"} {
			if _, ok := frame[key]; ok {
				payloads++
			}
		}
		if payloads != 1 {
			fail(protocolError(execution.ResponseFailureFrameInvalid))
			return
		}
		if seq == 0 {
			var version uint32
			if json.Unmarshal(frame["v"], &version) != nil || version != 1 {
				fail(protocolError(execution.ResponseFailureFormatUnsupported))
				return
			}
			if !send(sharedFrame{version: true, receivedAt: client.now().Unix()}) {
				return
			}
			continue
		}
		if _, again := frame["v"]; again {
			fail(protocolError(execution.ResponseFailureFrameInvalid))
			return
		}
		switch {
		case frame["schemas"] != nil:
			var declarations []json.RawMessage
			if json.Unmarshal(frame["schemas"], &declarations) != nil || declarations == nil || len(declarations) == 0 {
				fail(protocolError(execution.ResponseFailureSchemaInvalid))
				return
			}
			if len(declarations) > sharedSchemaCount-len(schemas) {
				fail(errDictionary)
				return
			}
			for _, raw := range declarations {
				if uint64(len(raw)) > sharedDictionaryBytes-dictionaryBytes {
					fail(errDictionary)
					return
				}
				dictionaryBytes += uint64(len(raw))
				fields, err := wireObject(raw)
				if err != nil {
					fail(protocolError(execution.ResponseFailureSchemaInvalid))
					return
				}
				var schema sharedSchema
				var id uint64
				if !wireUnsigned(fields["id"], &id) || fields["columns"] == nil || fields["types"] == nil || fields["group_keys"] == nil || json.Unmarshal(raw, &schema) != nil ||
					int(schema.ID) != len(schemas) || len(schema.Columns) == 0 || len(schema.Columns) != len(schema.Types) {
					fail(protocolError(execution.ResponseFailureSchemaInvalid))
					return
				}
				schemas = append(schemas, schema)
			}
		case frame["series"] != nil:
			var entries []json.RawMessage
			if json.Unmarshal(frame["series"], &entries) != nil || entries == nil || len(entries) == 0 {
				fail(protocolError(execution.ResponseFailureFrameInvalid))
				return
			}
			if len(entries) > sharedFrameSeries {
				fail(errFrameSeries)
				return
			}
			parsed := sharedFrame{series: make([]sharedSeries, 0, len(entries))}
			for _, raw := range entries {
				series, size, err := client.sharedSeriesOf(ctx, raw, schemas)
				if err != nil {
					parsed.err = err
					break
				}
				parsed.series = append(parsed.series, sharedSeries{series: series, size: size})
			}
			if !send(parsed) {
				return
			}
		case frame["end"] != nil:
			ended, err := wireObject(frame["end"])
			if err != nil {
				fail(err)
				return
			}
			footer = true
			if !send(sharedFrame{footer: ended}) {
				return
			}
		}
	}
}

// sharedSeriesOf is one series entry of a frame against the schemas
// declared so far, with its expanded size.
func (client *Client) sharedSeriesOf(ctx context.Context, raw json.RawMessage, schemas []sharedSchema) (responseSeries, uint64, error) {
	if err := ctx.Err(); err != nil {
		return responseSeries{}, 0, err
	}
	fields, err := sharedSeriesFields(raw)
	if err != nil {
		return responseSeries{}, 0, err
	}
	var id uint64
	var groups []json.RawMessage
	var rows [][]json.RawMessage
	if !wireUnsigned(fields[0], &id) || id >= uint64(len(schemas)) ||
		fields[1] == nil || json.Unmarshal(fields[1], &groups) != nil || fields[2] == nil || json.Unmarshal(fields[2], &rows) != nil {
		return responseSeries{}, 0, protocolError(execution.ResponseFailureSchemaInvalid)
	}
	schema := schemas[id]
	if len(groups) != len(schema.GroupKeys) {
		return responseSeries{}, 0, protocolError(execution.ResponseFailureSchemaInvalid)
	}
	for _, row := range rows {
		if len(row) != len(schema.Columns) {
			return responseSeries{}, 0, protocolError(execution.ResponseFailureSchemaInvalid)
		}
	}
	series := responseSeries{Columns: schema.Columns, Types: schema.Types, GroupKeys: schema.GroupKeys, GroupValues: groups, Values: rows}
	size, err := expandedSeriesBytes(series, uint64(client.limits.MaxSeriesBytes))
	if err != nil {
		return responseSeries{}, 0, err
	}
	return series, size, nil
}
