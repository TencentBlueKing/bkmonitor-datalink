// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"context"
	"fmt"
	"strconv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obevidence"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/platformsettings"
)

// StoreOperations exposes named domain readers, never caller-supplied keys or
// Redis commands. The producer owns both key construction and document meaning.
func StoreOperations(service *obevidence.Service) []Operation {
	id := Field{Type: "string", Description: "策略ID。", Source: "用户输入或 strategy.get", Pattern: "^[1-9][0-9]*$", MaxLength: 20}
	object := Field{Type: "string", Description: "运行对象ID。", Source: "strategy.get plans[].query_group", Pattern: "^[A-Za-z0-9_.:-]+$", MinLength: 1, MaxLength: 256}
	fields := enumField("平台动态配置字段。", platformsettings.Fields)
	limits := map[string]any{"max_commands": obevidence.MaxCommands, "max_document_bytes": obevidence.MaxDocumentBytes, "max_bytes": obevidence.MaxBytes, "deadline_ms": obevidence.ReadTimeout.Milliseconds()}
	configFields := map[string]Field{
		"view":        {Type: "string", Enum: []string{"source", "published"}, Description: "source 是当前源缓存；published 是指定不可变对象，不保证本 Slot 已采用。"},
		"strategy_id": id, "query_group": object,
		"object_digest": {Type: "string", Pattern: "^[a-fA-F0-9]{64}$", Description: "不可变发布对象摘要。", Source: "strategy.get plans[].object_digest"},
		"tenant":        {Type: "string", MaxLength: 256, MinLength: 1, Description: "发布计划的租户筛选。", Source: "strategy.get plans[].tenant"},
		"business":      {Type: "string", MaxLength: 256, MinLength: 1, Description: "发布计划的业务筛选。", Source: "strategy.get plans[].business"},
	}
	storeFields := map[string]Field{
		"family":      {Type: "string", Enum: []string{"source_strategy", "target_group", "dynamic_config", "query_progress", "query_cooldown"}, Description: "受支持的存储证据族；query_cooldown 是运行对象在降级池里的持久记录（入池时间、失败次数、上次出池及原因、写入者任期），重启或换持有者后按它恢复。"},
		"strategy_id": id, "query_group": object,
		"group_id": {Type: "string", Pattern: "^[^\\s\\x00-\\x1f\\x7f]+$", MinLength: 1, MaxLength: 512, Description: "目标组ID。", Source: "源策略 target 配置或既有目标组证据"},
		"fields":   {Type: "array", MaxItems: len(platformsettings.Fields), UniqueItems: true, Items: &fields, Description: "动态配置字段；省略或空数组时读取全部四项。"},
	}
	configVariants := map[string][]string{"source": {"strategy_id"}, "published": {"strategy_id", "query_group", "object_digest", "tenant", "business"}}
	storeVariants := map[string][]string{"source_strategy": {"strategy_id"}, "target_group": {"group_id"}, "dynamic_config": {"fields"}, "query_progress": {"query_group"}, "query_cooldown": {"query_group"}}
	configRequired := map[string][]string{"source": {"strategy_id"}, "published": {"strategy_id", "query_group", "object_digest"}}
	storeRequired := map[string][]string{"source_strategy": {"strategy_id"}, "target_group": {"group_id"}, "query_progress": {"query_group"}, "query_cooldown": {"query_group"}}
	ops := []Operation{
		{ID: "strategy.config", Summary: "读取策略具体配置，并区分当前源缓存与指定发布对象；秘密字段有明确省略记录。", Fields: configFields, Required: []string{"view", "strategy_id"}, Limits: limits, OutputSchema: SchemaOf(obevidence.Result{}), InputRules: variantRules("view", configFields, configVariants, configRequired), Examples: []Params{{"view": "source", "strategy_id": "1001"}}, Validate: func(p Params) error { return validateVariant(p, "view", configVariants, configRequired) }, Run: func(ctx context.Context, p Params) Outcome {
			return storeOutcome(service.StrategyConfig(ctx, obevidence.ConfigRequest{View: p.String("view"), StrategyID: p.String("strategy_id"), Tenant: p.String("tenant"), Business: p.String("business"), QueryGroup: p.String("query_group"), ObjectDigest: p.String("object_digest")}))
		}},
		{ID: "store.inspect", Summary: "按证据族和领域ID读取 Redis 类型、TTL、具体值与来源，缺失、错型、超限和不可达分别返回。", Fields: storeFields, Required: []string{"family"}, Limits: limits, OutputSchema: SchemaOf(obevidence.Result{}), InputRules: variantRules("family", storeFields, storeVariants, storeRequired), Examples: []Params{{"family": "dynamic_config"}, {"family": "source_strategy", "strategy_id": "1001"}}, Validate: func(p Params) error {
			if err := validateVariant(p, "family", storeVariants, storeRequired); err != nil {
				return err
			}
			seen := map[string]bool{}
			for _, field := range selectedFields(p) {
				if seen[string(field)] {
					return fmt.Errorf("fields must be unique")
				}
				seen[string(field)] = true
			}
			return nil
		}, Run: func(ctx context.Context, p Params) Outcome {
			return storeOutcome(service.Store(ctx, obevidence.StoreRequest{Family: p.String("family"), StrategyID: p.String("strategy_id"), GroupID: p.String("group_id"), QueryGroup: p.String("query_group"), Fields: selectedFields(p)}))
		}},
		{ID: "store.info", Summary: "读取 alarmd 用到的每个 Redis 的内存上限、淘汰策略、已用内存、淘汰与过期计数、各角色所在库的键数、脚本命令（eval/evalsha）累计调用与耗时、复制 offset 与各副本落后字节数（不含副本地址）（INFO 白名单字段）；读不到的服务器具名标出。", Fields: map[string]Field{}, Limits: limits, OutputSchema: SchemaOf(obevidence.InfoResult{}), Run: func(ctx context.Context, _ Params) Outcome {
			info := service.Info(ctx)
			out := Outcome{Value: info, Complete: info.Complete, Summary: strconv.Itoa(len(info.Servers)) + " Redis servers"}
			if !info.Complete {
				out.Limitations = append(out.Limitations, "Servers with status dependency_unavailable did not answer INFO; their fields are unknown, not zero.")
			}
			out.Limitations = append(out.Limitations, "commands counts every client of the server together, not alarmd alone, since its stats were last reset or it started; a script command absent from it had no calls in that time. A rate needs two reads.")
			return out
		}},
	}
	for i := range ops {
		ops[i].EvidenceScope = "replica_configured_store"
		ops[i].Targetable = true
	}
	return ops
}

func selectedFields(p Params) []platformsettings.Field {
	var fields []platformsettings.Field
	values, _ := p["fields"].([]any)
	for _, v := range values {
		if s, ok := v.(string); ok {
			fields = append(fields, platformsettings.Field(s))
		}
	}
	return fields
}

// Conditional schema and admission use the same variant tables.
func variantRules(selector string, fields map[string]Field, variants, required map[string][]string) []any {
	rules := make([]any, 0, len(variants))
	// Sort through field enum, not map iteration: the catalog digest is stable.
	for _, variant := range fields[selector].Enum {
		allowed := map[string]bool{selector: true}
		for _, name := range variants[variant] {
			allowed[name] = true
		}
		properties := map[string]any{}
		for name := range fields {
			if !allowed[name] {
				properties[name] = false
			}
		}
		rules = append(rules, map[string]any{"if": map[string]any{"properties": map[string]any{selector: map[string]any{"const": variant}}, "required": []string{selector}}, "then": map[string]any{"required": nonNil(required[variant]), "properties": properties}})
	}
	return rules
}
func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
func validateVariant(p Params, selector string, variants, required map[string][]string) error {
	variant := p.String(selector)
	allowed := map[string]bool{selector: true}
	for _, name := range variants[variant] {
		allowed[name] = true
	}
	for name := range p {
		if !allowed[name] {
			return fmt.Errorf("%s does not accept %s", variant, name)
		}
	}
	for _, name := range required[variant] {
		if _, ok := p[name]; !ok {
			return fmt.Errorf("%s requires %s", variant, name)
		}
	}
	if id := p.String("strategy_id"); id != "" {
		if _, err := strconv.ParseUint(id, 10, 64); err != nil {
			return fmt.Errorf("strategy_id exceeds the unsigned 64-bit range")
		}
	}
	return nil
}
func storeOutcome(r obevidence.Result) Outcome {
	out := Outcome{Value: r, Complete: r.Complete, Summary: r.Source + ": " + r.Status}
	if !r.Complete {
		out.Limitations = append(out.Limitations, "Store evidence status: "+r.Status+". Missing or unreadable evidence is not a healthy verdict.")
	}
	if len(r.Omitted) > 0 {
		out.Limitations = append(out.Limitations, "Value is an allowlisted projection; omitted paths and reasons are in result.omitted.")
	}
	switch r.Status {
	case "dependency_unavailable":
		out.Error = &Failure{"evidence_unavailable", "The configured evidence store could not be read."}
	case "invalid_input":
		out.Error = &Failure{"invalid_input", "The domain reader rejected the input."}
	}
	return out
}
