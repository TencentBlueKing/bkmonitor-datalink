// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package cli

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"unicode/utf8"
)

// Files are data only. A script is passed as the operation's registered stdin
// parameter; this CLI never starts a local shell or interprets its contents.
func (a *App) readInputFile(path string) ([]byte, error) {
	reader := a.In
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return nil, errors.New("cannot read input file")
		}
		defer f.Close()
		reader = f
	}
	data, err := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, errors.New("input file exceeds 1 MiB or could not be read")
	}
	return data, nil
}

func (a *App) attachStdin(contract, params map[string]any, path string) error {
	properties := objectField(objectField(contract, "input_schema"), "properties")
	name := ""
	var field map[string]any
	for key, value := range properties {
		candidate, _ := value.(map[string]any)
		if stringField(candidate, "parameter_source") == "stdin" {
			if name != "" {
				return errors.New("operation registers more than one stdin parameter")
			}
			name, field = key, candidate
		}
	}
	if name == "" || stringField(field, "type") != "string" {
		return errors.New("operation does not register a string stdin parameter; describe its supported input")
	}
	if _, exists := params[name]; exists {
		return errors.New("stdin parameter is already present in params")
	}
	data, err := a.readInputFile(path)
	if err != nil {
		return err
	}
	if !utf8.Valid(data) {
		return errors.New("stdin file must be valid UTF-8")
	}
	if maximum, ok := field["maxLength"].(json.Number); ok {
		max, err := maximum.Int64()
		if err != nil || max < 0 || int64(utf8.RuneCount(data)) > max {
			return errors.New("stdin file exceeds the described length limit")
		}
	}
	params[name] = string(data)
	return nil
}
