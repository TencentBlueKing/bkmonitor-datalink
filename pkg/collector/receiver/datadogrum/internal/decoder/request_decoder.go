// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package decoder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/datadogrum/internal/model"
)

var (
	// ErrInvalidLimit indicates a non-positive decoder limit.
	ErrInvalidLimit = errors.New("invalid decoder limit")
	// ErrBodyTooLarge indicates that the decompressed request body exceeds its limit.
	ErrBodyTooLarge = errors.New("request body too large")
	// ErrDecodedEventsTooLarge indicates that retained event JSON exceeds its limit.
	ErrDecodedEventsTooLarge = errors.New("decoded events too large")
)

// decodedSizeLimiter tracks the total size of events retained by the decoder.
// It is not safe for concurrent use; each DecodeEvents call must use its own instance.
type decodedSizeLimiter struct {
	maxBytes  int64
	usedBytes int64
}

// newDecodedSizeLimiter creates a limiter for retained event JSON.
func newDecodedSizeLimiter(maxBytes int64) decodedSizeLimiter {
	return decodedSizeLimiter{maxBytes: maxBytes}
}

func (l *decodedSizeLimiter) add(eventSize int) error {
	if eventSize < 0 || int64(eventSize) > l.maxBytes-l.usedBytes {
		return fmt.Errorf("%w: exceeds %d bytes", ErrDecodedEventsTooLarge, l.maxBytes)
	}
	l.usedBytes += int64(eventSize)
	return nil
}

// DecodeEvents splits a request body into raw RUM event objects.
// It accepts JSON arrays, wrapped events, single objects, NDJSON, and JSON streams.
func DecodeEvents(data []byte, maxDecodedBytes int64) ([][]byte, error) {
	if maxDecodedBytes <= 0 {
		return nil, ErrInvalidLimit
	}

	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, model.ErrEmptyBody
	}

	if trimmed[0] == '[' {
		return decodeArray(trimmed, maxDecodedBytes)
	}
	if events, ok, err := decodeNDJSON(trimmed, maxDecodedBytes); err != nil || ok {
		return events, err
	}
	events, ok, err := decodeObject(trimmed, maxDecodedBytes)
	if err != nil {
		return nil, err
	}
	if ok {
		return events, nil
	}
	return decodeStream(trimmed, maxDecodedBytes)
}

func decodeNDJSON(data []byte, maxDecodedBytes int64) ([][]byte, bool, error) {
	if !bytes.ContainsAny(data, "\r\n") {
		return nil, false, nil
	}

	events := make([][]byte, 0, bytes.Count(data, []byte{'\n'})+1)
	limiter := newDecodedSizeLimiter(maxDecodedBytes)
	for start := 0; start <= len(data); {
		end := start
		if idx := bytes.IndexByte(data[start:], '\n'); idx >= 0 {
			end = start + idx
		} else {
			end = len(data)
		}

		line := bytes.TrimSpace(data[start:end])
		if len(line) > 0 {
			if line[0] != '{' || line[len(line)-1] != '}' {
				return nil, false, nil
			}
			if !json.Valid(line) {
				return nil, true, model.ErrInvalidPayload
			}
			if err := limiter.add(len(line)); err != nil {
				return nil, true, err
			}
			events = append(events, line)
		}

		if end == len(data) {
			break
		}
		start = end + 1
	}

	if len(events) <= 1 {
		return nil, false, nil
	}
	return events, true, nil
}

func decodeArray(data []byte, maxDecodedBytes int64) ([][]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	events, err := decodeJSONArray(dec, maxDecodedBytes)
	if err != nil {
		return nil, err
	}
	if err := expectEOF(dec); err != nil {
		return nil, model.ErrInvalidPayload
	}
	return events, nil
}

func decodeObject(data []byte, maxDecodedBytes int64) ([][]byte, bool, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := expectToken(dec, '{'); err != nil {
		return nil, false, nil
	}

	var (
		events    [][]byte
		hasEvents bool
		hasType   bool
		typeRaw   json.RawMessage
	)
	for dec.More() {
		key, err := decodeObjectKey(dec)
		if err != nil {
			return nil, true, model.ErrInvalidPayload
		}

		switch key {
		case "events":
			if hasEvents {
				return nil, true, model.ErrInvalidPayload
			}
			hasEvents = true
			events, err = decodeJSONArray(dec, maxDecodedBytes)
			if err != nil {
				return nil, true, err
			}
		case "type":
			if hasType {
				return nil, true, model.ErrInvalidPayload
			}
			hasType = true
			if err := dec.Decode(&typeRaw); err != nil {
				return nil, true, model.ErrInvalidPayload
			}
		default:
			var ignored json.RawMessage
			if err := dec.Decode(&ignored); err != nil {
				return nil, true, model.ErrInvalidPayload
			}
		}
	}
	if err := expectToken(dec, '}'); err != nil {
		return nil, true, model.ErrInvalidPayload
	}
	if err := expectEOF(dec); err != nil {
		return nil, false, nil
	}

	if hasEvents {
		return events, true, nil
	}
	if !hasType {
		return nil, true, model.ErrInvalidPayload
	}
	if err := validateEventType(typeRaw); err != nil {
		return nil, true, err
	}
	if err := ensureDecodedEventSize(len(data), maxDecodedBytes); err != nil {
		return nil, true, err
	}
	return [][]byte{append([]byte(nil), data...)}, true, nil
}

func decodeStream(data []byte, maxDecodedBytes int64) ([][]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	events := make([][]byte, 0)
	limiter := newDecodedSizeLimiter(maxDecodedBytes)
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			return nil, model.ErrInvalidPayload
		}
		if !isJSONObject(raw) {
			return nil, model.ErrInvalidPayload
		}
		if err := limiter.add(len(raw)); err != nil {
			return nil, err
		}
		events = append(events, append([]byte(nil), raw...))
	}
	if len(events) == 0 {
		return nil, model.ErrEmptyBody
	}
	return events, nil
}

func decodeJSONArray(dec *json.Decoder, maxDecodedBytes int64) ([][]byte, error) {
	if err := expectToken(dec, '['); err != nil {
		return nil, model.ErrInvalidPayload
	}

	limiter := newDecodedSizeLimiter(maxDecodedBytes)
	events := make([][]byte, 0)
	for dec.More() {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, model.ErrInvalidPayload
		}
		if !isJSONObject(raw) {
			return nil, model.ErrInvalidPayload
		}
		if err := limiter.add(len(raw)); err != nil {
			return nil, err
		}
		events = append(events, append([]byte(nil), raw...))
	}
	if err := expectToken(dec, ']'); err != nil {
		return nil, model.ErrInvalidPayload
	}
	if len(events) == 0 {
		return nil, model.ErrEmptyBody
	}
	return events, nil
}

func decodeObjectKey(dec *json.Decoder) (string, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", err
	}
	key, ok := tok.(string)
	if !ok {
		return "", errors.New("object key is not a string")
	}
	return key, nil
}

func expectToken(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != want {
		return fmt.Errorf("expected JSON delimiter %q", want)
	}
	return nil
}

func expectEOF(dec *json.Decoder) error {
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func isJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) >= 2 && trimmed[0] == '{' && trimmed[len(trimmed)-1] == '}'
}

// validateEventType verifies that a single-event object has a string type field.
func validateEventType(rawType json.RawMessage) error {
	var eventType string
	if err := json.Unmarshal(rawType, &eventType); err != nil {
		return model.ErrInvalidPayload
	}
	return nil
}

func ensureDecodedEventSize(size int, max int64) error {
	if max <= 0 {
		return ErrInvalidLimit
	}
	if int64(size) > max {
		return fmt.Errorf("%w: exceeds %d bytes", ErrDecodedEventsTooLarge, max)
	}
	return nil
}
