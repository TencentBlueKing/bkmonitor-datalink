// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"encoding"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	yamlUnmarshaler = reflect.TypeOf((*yaml.Unmarshaler)(nil)).Elem()
	textUnmarshaler = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
)

// strictIntegers holds every integer key of the document to a whole number
// written in decimal, bare or quoted, and refuses anything else by the key's
// path. It walks the document against the type it is about to be decoded
// into, before the decoder sees it.
//
// The decoder alone takes a float into an integer by truncating it: a key
// written 1.5 loads as 1, a value nobody chose, and a budget, a bound or a
// horizon that is not what its operator wrote. It refuses words only by line
// number, and a quoted number not at all, though a rendered values file may
// quote one. A quoted decimal is rewritten in place to the integer it spells,
// and the caller re-encodes the document when anything was; every other key,
// unknown ones included, is left to the decoder, which refuses unknown keys.
//
// A type that decodes itself (a Duration reads "30s") is its own reader and is
// not walked.
func strictIntegers(node *yaml.Node, target reflect.Type, path string) (bool, error) {
	if node == nil {
		return false, nil
	}
	switch node.Kind {
	case yaml.DocumentNode:
		rewritten := false
		for _, content := range node.Content {
			changed, err := strictIntegers(content, target, path)
			if err != nil {
				return false, err
			}
			rewritten = rewritten || changed
		}
		return rewritten, nil
	case yaml.AliasNode:
		return strictIntegers(node.Alias, target, path)
	}
	for target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	if reflect.PointerTo(target).Implements(yamlUnmarshaler) || reflect.PointerTo(target).Implements(textUnmarshaler) {
		return false, nil
	}
	switch target.Kind() {
	case reflect.Struct:
		if node.Kind != yaml.MappingNode {
			return false, nil
		}
		fields := map[string]reflect.Type{}
		collectYAMLFields(target, fields)
		rewritten := false
		for index := 0; index+1 < len(node.Content); index += 2 {
			key := node.Content[index].Value
			field, known := fields[key]
			if !known {
				continue
			}
			changed, err := strictIntegers(node.Content[index+1], field, joinPath(path, key))
			if err != nil {
				return false, err
			}
			rewritten = rewritten || changed
		}
		return rewritten, nil
	case reflect.Map:
		if node.Kind != yaml.MappingNode {
			return false, nil
		}
		rewritten := false
		for index := 0; index+1 < len(node.Content); index += 2 {
			changed, err := strictIntegers(node.Content[index+1], target.Elem(), joinPath(path, node.Content[index].Value))
			if err != nil {
				return false, err
			}
			rewritten = rewritten || changed
		}
		return rewritten, nil
	case reflect.Slice, reflect.Array:
		if node.Kind != yaml.SequenceNode {
			return false, nil
		}
		rewritten := false
		for index, element := range node.Content {
			changed, err := strictIntegers(element, target.Elem(), fmt.Sprintf("%s[%d]", path, index))
			if err != nil {
				return false, err
			}
			rewritten = rewritten || changed
		}
		return rewritten, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strictInteger(node, target, path)
	}
	return false, nil
}

func strictInteger(node *yaml.Node, target reflect.Type, path string) (bool, error) {
	tag := node.ShortTag()
	if node.Kind != yaml.ScalarNode || tag == "!!null" {
		return false, nil
	}
	text := strings.TrimSpace(node.Value)
	var err error
	switch target.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		_, err = strconv.ParseUint(text, 10, target.Bits())
	default:
		_, err = strconv.ParseInt(text, 10, target.Bits())
	}
	// The text decides, not the tag: a float, a bool or words never parse
	// as a decimal, and a quoted or explicitly tagged decimal is the number
	// it spells.
	if err != nil {
		return false, fmt.Errorf("%s %q (line %d) must be a whole number written in decimal, within its type's range",
			path, node.Value, node.Line)
	}
	if tag != "!!int" || text != node.Value {
		node.Tag, node.Style, node.Value = "!!int", 0, text
		return true, nil
	}
	return false, nil
}

// collectYAMLFields maps each key the decoder reads into the struct to its
// field's type, the inline structs' keys included, by yaml.v3's naming: the
// tag's name, else the field's name in lower case.
func collectYAMLFields(target reflect.Type, fields map[string]reflect.Type) {
	for index := 0; index < target.NumField(); index++ {
		field := target.Field(index)
		// The decoder reads an embedded struct whatever its name's case,
		// and no other unexported field.
		if !field.IsExported() && !field.Anonymous {
			continue
		}
		tag := field.Tag.Get("yaml")
		name, options, _ := strings.Cut(tag, ",")
		if name == "-" {
			continue
		}
		if strings.Contains(","+options+",", ",inline,") {
			inline := field.Type
			for inline.Kind() == reflect.Pointer {
				inline = inline.Elem()
			}
			if inline.Kind() == reflect.Struct {
				collectYAMLFields(inline, fields)
			}
			continue
		}
		if name == "" {
			name = strings.ToLower(field.Name)
		}
		fields[name] = field.Type
	}
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}
