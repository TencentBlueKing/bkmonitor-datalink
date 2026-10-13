// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package docs

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

func readSwaggerDocument(t *testing.T) map[string]any {
	t.Helper()
	body, err := os.ReadFile("swagger.json")
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, json.Unmarshal(body, &document))
	return document
}

func schemaObject(t *testing.T, value any) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	require.True(t, ok, "expected schema object, got %T", value)
	return result
}

func resolveSwaggerSchema(t *testing.T, document map[string]any, schema map[string]any) map[string]any {
	t.Helper()
	if ref, ok := schema["$ref"].(string); ok {
		require.True(t, strings.HasPrefix(ref, "#/definitions/"), ref)
		definitions := schemaObject(t, document["definitions"])
		return schemaObject(t, definitions[strings.TrimPrefix(ref, "#/definitions/")])
	}
	return schema
}

func TestTimeGraphSwaggerArtifactsAgree(t *testing.T) {
	want := readSwaggerDocument(t)
	for name, body := range map[string][]byte{
		"registered Go document": []byte(SwaggerInfo.ReadDoc()),
		"YAML": func() []byte {
			body, err := os.ReadFile("swagger.yaml")
			require.NoError(t, err)
			var document map[string]any
			require.NoError(t, yaml.Unmarshal(body, &document))
			encoded, err := json.Marshal(document)
			require.NoError(t, err)
			return encoded
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			var got map[string]any
			require.NoError(t, json.Unmarshal(body, &got))
			// The runtime template exposes these configurable, initially empty fields.
			if got["host"] == "" {
				delete(got, "host")
			}
			if schemes, ok := got["schemes"].([]any); ok && len(schemes) == 0 {
				delete(got, "schemes")
			}
			require.Equal(t, want, got)
		})
	}
}

func TestTimeGraphSwaggerPublicFieldsAndRoutes(t *testing.T) {
	document := readSwaggerDocument(t)
	definitions := schemaObject(t, document["definitions"])
	for _, value := range []any{
		cmdb.SharedTopologyQuery{},
		cmdb.RelationMultiResourceResponseData{},
		cmdb.RelationMultiResourceRangeResponseData{},
		cmdb.RelationPathResourcesResponseData{},
		cmdb.RelationPathResourcesRangeResponseData{},
		cmdb.CompactTopology{},
		cmdb.CompactTopologyNode{},
		cmdb.CompactTopologyEdge{},
		cmdb.CompactTopologyPartial{},
	} {
		typeOf := reflect.TypeOf(value)
		t.Run(typeOf.Name(), func(t *testing.T) {
			properties := schemaObject(t, schemaObject(t, definitions["cmdb."+typeOf.Name()])["properties"])
			for index := 0; index < typeOf.NumField(); index++ {
				name := strings.Split(typeOf.Field(index).Tag.Get("json"), ",")[0]
				if name != "" && name != "-" {
					require.Contains(t, properties, name, "published field missing from schema")
				}
			}
		})
	}
	paths := schemaObject(t, document["paths"])
	for _, path := range []string{
		"/api/v1/relation/path_resources", "/api/v1/relation/path_resources_range",
		"/api/v1/relation/v1beta3/multi_resource", "/api/v1/relation/v1beta3/multi_resource_range",
		"/api/v1/relation/v1beta3/topology", "/api/v1/relation/v1beta3/topology_range",
	} {
		operation := schemaObject(t, schemaObject(t, paths[path])["post"])
		require.NotEmpty(t, operation["operationId"])
		responses := schemaObject(t, operation["responses"])
		response := schemaObject(t, responses["200"])
		require.NotEmpty(t, resolveSwaggerSchema(t, document, schemaObject(t, response["schema"])))
	}
	for name, fields := range map[string][]string{
		"cmdb.CompactTopologyNode": {"id"}, "cmdb.CompactTopologyEdge": {"source", "target"},
	} {
		properties := schemaObject(t, schemaObject(t, definitions[name])["properties"])
		for _, field := range fields {
			require.Equal(t, "string", schemaObject(t, properties[field])["type"], "compact identities must retain uint64 precision")
		}
	}
	queryProperties := schemaObject(t, schemaObject(t, definitions["cmdb.SharedTopologyQuery"])["properties"])
	require.ElementsMatch(t, []any{"snapshots", cmdb.CompactTopologyFormat}, schemaObject(t, queryProperties["response_format"])["enum"])
}

// checkWireFields follows the published references for the concrete response.
// It checks field coverage and shapes, including a nullable compact error result.
func checkWireFields(t *testing.T, document, schema map[string]any, value any, path string) {
	t.Helper()
	if value == nil {
		require.Equal(t, true, schema["x-nullable"], "unexpected null at %s", path)
		return
	}
	schema = resolveSwaggerSchema(t, document, schema)
	if alternatives, ok := schema["allOf"].([]any); ok {
		for _, alternative := range alternatives {
			checkWireFields(t, document, schemaObject(t, alternative), value, path)
		}
		return
	}
	switch schema["type"] {
	case "object":
		object := schemaObject(t, value)
		properties, _ := schema["properties"].(map[string]any)
		for key, child := range object {
			childSchema, ok := properties[key].(map[string]any)
			if !ok {
				childSchema, ok = schema["additionalProperties"].(map[string]any)
			}
			require.True(t, ok, "wire field absent from schema: %s.%s", path, key)
			checkWireFields(t, document, childSchema, child, path+"."+key)
		}
	case "array":
		array, ok := value.([]any)
		require.True(t, ok, path)
		for index, child := range array {
			checkWireFields(t, document, schemaObject(t, schema["items"]), child, fmt.Sprintf("%s[%d]", path, index))
		}
	case "string":
		require.IsType(t, "", value, path)
	case "integer", "number":
		require.IsType(t, float64(0), value, path)
	case "boolean":
		require.IsType(t, false, value, path)
	default:
		t.Fatalf("unsupported schema at %s: %+v", path, schema)
	}
}

func TestTimeGraphSwaggerDescribesMixedWireResponse(t *testing.T) {
	document := readSwaggerDocument(t)
	// Use production response structs, including their distinct omission rules.
	payload := struct {
		TraceID string `json:"trace_id"`
		Data    []any  `json:"data"`
	}{Data: []any{
		cmdb.SharedTopologyResponseData{Code: 200, PointCount: 1, Snapshots: []cmdb.SharedTopologySnapshot{{
			Timestamp: 1000, Nodes: []cmdb.SharedTopologyNode{{ID: 42, ResourceType: "node", Dimensions: cmdb.Matcher{"id": "root"}}},
			Edges: []cmdb.SharedTopologyEdge{}, Partial: true, PartialReason: "backend_partial",
		}}},
		cmdb.CompactTopologyResponseData{Code: 200, PointCount: 1, ResponseFormat: cmdb.CompactTopologyFormat, Compact: &cmdb.CompactTopology{
			Version: 1, Timestamps: []int64{1000}, Nodes: []cmdb.CompactTopologyNode{{ID: "18446744073709551615", ResourceType: "node", Dimensions: cmdb.Matcher{"id": "root"}, Mask: []string{"0000000000000001"}}},
			Edges: []cmdb.CompactTopologyEdge{}, Partial: []cmdb.CompactTopologyPartial{}, NodeOccurrences: 1,
		}},
		cmdb.CompactTopologyResponseData{Code: 400, ResponseFormat: cmdb.CompactTopologyFormat, Message: "invalid request"},
		cmdb.SharedTopologyResponseData{Code: 400, Message: "invalid request", Snapshots: []cmdb.SharedTopologySnapshot{}},
	}}
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(body, &wire))
	paths := schemaObject(t, document["paths"])
	for _, route := range []string{"/api/v1/relation/v1beta3/topology", "/api/v1/relation/v1beta3/topology_range"} {
		operation := schemaObject(t, schemaObject(t, paths[route])["post"])
		response := schemaObject(t, schemaObject(t, operation["responses"])["200"])
		checkWireFields(t, document, schemaObject(t, response["schema"]), wire, route)
	}
}
