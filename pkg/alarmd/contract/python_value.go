package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// PythonValueMD5 applies the existing Python count_md5 protocol to one JSON
// value, including dictionaries used by legacy record identity.
func PythonValueMD5(raw json.RawMessage) (string, error) {
	if !json.Valid(raw) {
		return "", fmt.Errorf("invalid JSON value")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	return pythonCountMD5(value)
}

// PythonScalarText preserves Python str formatting for observed scalar values.
func PythonScalarText(raw json.RawMessage) (string, error) {
	value, err := monitorScalar(raw)
	if err != nil {
		return "", err
	}
	return pythonScalarString(value)
}
