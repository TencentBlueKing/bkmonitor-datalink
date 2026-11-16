// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

// Run from pkg/unify-query: go run ./docs/examples/schema-catalog
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

func main() {
	resources := relation.DefaultResourceDefinitions()
	relations := relation.DefaultRelationDefinitions()
	catalog := struct {
		Resources []*relation.ResourceDefinition `json:"resources"`
		Relations []*relation.RelationDefinition `json:"relations"`
	}{resources, relations}
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile("docs/api/timegraph-schema.json", append(data, '\n'), 0o644); err != nil {
		panic(err)
	}
	var doc strings.Builder
	doc.WriteString("# TimeGraph 默认资源与关系目录\n\n此文件由 `go run ./docs/examples/schema-catalog` 从同一代码版本的默认 Schema 生成。\nRedis 的空间定义或部署覆盖可能不同；先取得实际 Schema，再使用完整主键对齐实体。\n定义存在不代表指标已写入、当前空间有路由或容量已验收。扩展及接入前提见 [使用指南](timegraph.md#schema-and-routing)。\n\n")
	fmt.Fprintf(&doc, "## 资源（%d）\n\n所有请求和响应 dimensions 值均为字符串。字段类型是 Schema 语义；不能用可选展示字段构造实体身份。\n\n| 类型 | 完整主键 | 可选属性 | 非字符串字段类型 |\n| --- | --- | --- | --- |\n", len(resources))
	for _, r := range resources {
		optional, types := []string{}, []string{}
		for _, f := range r.Fields {
			if !f.Required {
				optional = append(optional, f.Name)
			}
			if f.Type != "" && f.Type != relation.FieldTypeString {
				types = append(types, f.Name+":"+f.Type)
			}
		}
		fmt.Fprintf(&doc, "| %s | %s | %s | %s |\n", r.Name, strings.Join(r.GetPrimaryKeys(), ", "), strings.Join(optional, ", "), strings.Join(types, ", "))
	}
	fmt.Fprintf(&doc, "\n## 关系（%d）\n\n静态双向关系可沿两向遍历；定向关系按请求的 both/outbound/inbound 选方向。物理指标列按显式 metric_name 或默认命名规则生成；返回的 metric_name 允许为空，表示未显式覆盖。\n\n| 关系类型 | 起点 | 终点 | 类别 | 默认方向 | 物理指标 |\n| --- | --- | --- | --- | --- | --- |\n", len(relations))
	for _, r := range relations {
		category, direction, metric := r.Category, "bidirectional", r.GetRelationName()
		if category == "" {
			category = "static"
		}
		if r.IsDirectional {
			direction = "outbound"
		}
		if override := r.Labels["metric_name"]; override != "" {
			metric = override
		}
		fmt.Fprintf(&doc, "| %s | %s | %s | %s | %s | %s |\n", r.Name, r.FromResource, r.ToResource, category, direction, metric)
	}
	if err := os.WriteFile("docs/api/timegraph-schema.md", []byte(doc.String()), 0o644); err != nil {
		panic(err)
	}
}
