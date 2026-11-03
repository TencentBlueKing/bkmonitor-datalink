package contract

import (
	"bytes"
	"crypto/md5" // Python's existing dedupe protocol uses MD5, not a security primitive.
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

// PythonDedupeMD5 implements bkmonitor.utils.common_utils.count_md5 for the
// values already selected by Python-compatible cleaned dedupe keys. Field
// selection, target construction and EventID are deliberately outside this API.
// Raw JSON preserves Python's distinction between integers and floats.
// Event._clean_tags must already have converted dict tags (and container list
// elements) with Python json.dumps before callers supply these values.
func PythonDedupeMD5(values []json.RawMessage) (string, error) {
	decoded := make([]any, len(values))
	for i, raw := range values {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&decoded[i]); err != nil {
			return "", fmt.Errorf("python dedupe value %d: %w", i, err)
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return "", fmt.Errorf("python dedupe value %d: expected one JSON value", i)
		}
	}
	return pythonCountMD5(decoded)
}

func pythonCountMD5(value any) (string, error) {
	var text string
	switch value := value.(type) {
	case nil:
		text = "None"
	case bool:
		text = "False"
		if value {
			text = "True"
		}
	case string:
		text = value
	case json.Number:
		var err error
		text, err = pythonJSONNumberString(value)
		if err != nil {
			return "", err
		}
	case []any:
		hashes := make([]string, len(value))
		for i, item := range value {
			var err error
			hashes[i], err = pythonCountMD5(item)
			if err != nil {
				return "", err
			}
		}
		sort.Strings(hashes)
		// Python str(list) uses single quotes and comma-space. Every element
		// here is an ASCII MD5 hex string, so no Python repr escaping is needed.
		text = "[]"
		if len(hashes) > 0 {
			text = "['" + strings.Join(hashes, "', '") + "']"
		}
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		pairs := make([]any, 0, len(keys))
		for _, key := range keys {
			hash, err := pythonCountMD5(value[key])
			if err != nil {
				return "", err
			}
			pairs = append(pairs, []any{key, hash})
		}
		return pythonCountMD5(pairs)
	default:
		return "", fmt.Errorf("python dedupe: unsupported JSON value %T", value)
	}
	digest := md5.Sum([]byte(text))
	return hex.EncodeToString(digest[:]), nil
}

func pythonJSONNumberString(number json.Number) (string, error) {
	text := number.String()
	if !strings.ContainsAny(text, ".eE") {
		integer, ok := new(big.Int).SetString(text, 10)
		if !ok {
			return "", fmt.Errorf("python dedupe: invalid integer %q", text)
		}
		return integer.String(), nil
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		if numberError, ok := err.(*strconv.NumError); !ok || numberError.Err != strconv.ErrRange {
			return "", fmt.Errorf("python dedupe: invalid float %q: %w", text, err)
		}
	}
	if math.IsInf(value, 1) {
		return "inf", nil
	}
	if math.IsInf(value, -1) {
		return "-inf", nil
	}
	// CPython uses fixed notation for decimal exponents [-4, 16).
	format := byte('f')
	if abs := math.Abs(value); abs != 0 && (abs < 1e-4 || abs >= 1e16) {
		format = 'e'
	}
	result := strconv.FormatFloat(value, format, -1, 64)
	if format == 'f' && !strings.Contains(result, ".") {
		result += ".0"
	}
	return result, nil
}
