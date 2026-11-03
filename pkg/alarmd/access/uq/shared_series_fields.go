package uq

import (
	"bytes"
	"encoding/json"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// sharedSeriesFields visits a series object already validated by the frame's
// json.Unmarshal into []json.RawMessage. It borrows field slices only while
// the frame is consumed; g/r are still unmarshalled into owning RawMessages
// before normalization and delivery. Avoiding Token and a map per series is
// important for large responses, without changing duplicate-field semantics.
func sharedSeriesFields(raw []byte) ([3]json.RawMessage, error) {
	var fields [3]json.RawMessage
	raw = bytes.TrimSpace(raw)
	if len(raw) < 2 || raw[0] != '{' || raw[len(raw)-1] != '}' {
		return fields, protocolError(execution.ResponseFailureFrameInvalid)
	}
	var unknown map[string]struct{}
	for at := 1; ; {
		at = jsonSpaceEnd(raw, at)
		if at >= len(raw) || raw[at] == '}' {
			break
		}
		end := jsonStringEnd(raw, at)
		if end == 0 {
			return fields, protocolError(execution.ResponseFailureFrameInvalid)
		}
		key := raw[at:end]
		index := -1
		if len(key) == 3 {
			switch key[1] {
			case 's':
				index = 0
			case 'g':
				index = 1
			case 'r':
				index = 2
			}
		}
		var name string
		if index < 0 {
			if json.Unmarshal(key, &name) != nil {
				return fields, protocolError(execution.ResponseFailureFrameInvalid)
			}
			switch name {
			case "s":
				index = 0
			case "g":
				index = 1
			case "r":
				index = 2
			}
		}
		at = jsonSpaceEnd(raw, end)
		if at >= len(raw) || raw[at] != ':' {
			return fields, protocolError(execution.ResponseFailureFrameInvalid)
		}
		at = jsonSpaceEnd(raw, at+1)
		end = jsonValueEnd(raw, at)
		if end <= at {
			return fields, protocolError(execution.ResponseFailureFrameInvalid)
		}
		if index >= 0 {
			if fields[index] != nil {
				return fields, protocolError(execution.ResponseFailureFrameInvalid)
			}
			fields[index] = raw[at:end]
		} else {
			if unknown == nil {
				unknown = make(map[string]struct{})
			}
			if _, duplicate := unknown[name]; duplicate {
				return fields, protocolError(execution.ResponseFailureFrameInvalid)
			}
			unknown[name] = struct{}{}
		}
		at = jsonSpaceEnd(raw, end)
		if at < len(raw) && raw[at] == ',' {
			at++
			continue
		}
		if at != len(raw)-1 || raw[at] != '}' {
			return fields, protocolError(execution.ResponseFailureFrameInvalid)
		}
		break
	}
	return fields, nil
}

func jsonSpaceEnd(raw []byte, at int) int {
	for at < len(raw) {
		switch raw[at] {
		case ' ', '\n', '\r', '\t':
			at++
		default:
			return at
		}
	}
	return at
}

// The scanner does not replace JSON validation. Its caller owns valid JSON;
// it only locates encoded values, respecting string escapes and nesting.
func jsonStringEnd(raw []byte, at int) int {
	if at >= len(raw) || raw[at] != '"' {
		return 0
	}
	for at++; at < len(raw); at++ {
		if raw[at] == '\\' {
			at++
		} else if raw[at] == '"' {
			return at + 1
		}
	}
	return 0
}

func jsonValueEnd(raw []byte, at int) int {
	if at >= len(raw) {
		return 0
	}
	if raw[at] == '"' {
		return jsonStringEnd(raw, at)
	}
	if raw[at] == '[' || raw[at] == '{' {
		depth := 0
		for at < len(raw) {
			switch raw[at] {
			case '"':
				at = jsonStringEnd(raw, at)
				if at == 0 {
					return 0
				}
				continue
			case '[', '{':
				depth++
			case ']', '}':
				depth--
				if depth == 0 {
					return at + 1
				}
			}
			at++
		}
		return 0
	}
	for at < len(raw) {
		switch raw[at] {
		case ',', '}', ']', ' ', '\n', '\r', '\t':
			return at
		}
		at++
	}
	return at
}
