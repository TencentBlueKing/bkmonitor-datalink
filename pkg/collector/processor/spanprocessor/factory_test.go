// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package spanprocessor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/fields"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
)

func TestFactory(t *testing.T) {
	content := `
processor:
  - name: "span_processor/common"
    config:
      drop:
        - match_rules:
            - key: "span_name"
              op: "eq"
              value:
                - "aaa"
`
	mainConf := processor.MustLoadConfigs(content)[0].Config

	customContent := `
processor:
  - name: "span_processor/common"
    config:
      drop:
        - match_rules:
            - key: "span_name"
              op: "contains"
              value:
                - "bbb"
`
	customConf := processor.MustLoadConfigs(customContent)[0].Config

	obj, err := NewFactory(mainConf, []processor.SubConfigProcessor{
		{
			Token: "token1",
			Type:  define.SubConfigFieldDefault,
			Config: processor.Config{
				Config: customConf,
			},
		},
	})
	assert.NoError(t, err)
	if err != nil {
		return
	}
	factory := obj.(*spanProcessor)
	assert.Equal(t, mainConf, factory.MainConfig())
	assert.Equal(t, customConf, factory.SubConfigs()[0].Config.Config)

	var mc Config
	assert.NoError(t, mapstructure.Decode(mainConf, &mc))
	assert.Equal(t, mc, factory.configs.GetGlobal().(Config))

	var cc Config
	assert.NoError(t, mapstructure.Decode(customConf, &cc))
	assert.Equal(t, cc, factory.configs.GetByToken("token1").(Config))

	assert.Equal(t, define.ProcessorSpanProcessor, factory.Name())
	assert.False(t, factory.IsDerived())
	assert.False(t, factory.IsPreCheck())

	factory.Reload(mainConf, nil)
	assert.Equal(t, mainConf, factory.MainConfig())
}

func TestNewFactory(t *testing.T) {
	t.Run("Returns error when main config cannot be decoded", func(t *testing.T) {
		obj, err := newFactory(map[string]any{"drop": "invalid"}, nil)
		assert.Error(t, err)
		assert.Nil(t, obj)
	})

	t.Run("Skips invalid customized configs and keeps global config available", func(t *testing.T) {
		content := `
processor:
  - name: "span_processor/common"
    config:
      drop:
        - match_rules:
            - key: "span_name"
              op: "eq"
              value:
                - "keep-me"
`
		mainConf := processor.MustLoadConfigs(content)[0].Config

		obj, err := newFactory(mainConf, []processor.SubConfigProcessor{
			{
				Token: "token-bad",
				Type:  define.SubConfigFieldDefault,
				Config: processor.Config{
					Config: map[string]any{"drop": "invalid"},
				},
			},
		})
		assert.NoError(t, err)
		if err != nil {
			return
		}

		var global Config
		assert.NoError(t, mapstructure.Decode(mainConf, &global))
		assert.Equal(t, global, obj.configs.GetGlobal().(Config))
		assert.Equal(t, global, obj.configs.GetByToken("token-bad").(Config))
		assert.Len(t, obj.configs.All(), 1)
	})
}

func TestReload(t *testing.T) {
	t.Run("Replaces processor state when reload config is valid", func(t *testing.T) {
		mainContent := `
processor:
  - name: "span_processor/common"
    config:
      drop:
        - match_rules:
            - key: "span_name"
              op: "eq"
              value:
                - "before"
`
		reloadContent := `
processor:
  - name: "span_processor/common"
    config:
      replace_value:
        - predicate_key: "span_name"
          rules:
            - filters:
                - key: "span_name"
                  op: "eq"
                  value:
                    - "after"
              replace_from:
                const_val: "reloaded"
`
		customContent := `
processor:
  - name: "span_processor/common"
    config:
      drop:
        - match_rules:
            - key: "span_name"
              op: "eq"
              value:
                - "token-only"
`

		obj, err := newFactory(processor.MustLoadConfigs(mainContent)[0].Config, nil)
		assert.NoError(t, err)
		if err != nil {
			return
		}

		oldConfigs := obj.configs
		oldMainConfig := obj.MainConfig()

		reloadedConf := processor.MustLoadConfigs(reloadContent)[0].Config
		obj.Reload(reloadedConf, []processor.SubConfigProcessor{{
			Token: "token1",
			Type:  define.SubConfigFieldDefault,
			Config: processor.Config{
				Config: processor.MustLoadConfigs(customContent)[0].Config,
			},
		}})

		assert.Equal(t, reloadedConf, obj.MainConfig())
		assert.NotSame(t, oldConfigs, obj.configs)
		assert.NotEqual(t, oldMainConfig, obj.MainConfig())
		assert.Equal(t, fields.NewSpanFieldFetcher(), obj.fetcher)

		var expectedGlobal Config
		assert.NoError(t, mapstructure.Decode(reloadedConf, &expectedGlobal))
		assert.Equal(t, expectedGlobal, obj.configs.GetGlobal().(Config))

		var expectedCustom Config
		assert.NoError(t, mapstructure.Decode(processor.MustLoadConfigs(customContent)[0].Config, &expectedCustom))
		assert.Equal(t, expectedCustom, obj.configs.GetByToken("token1").(Config))
		assert.Len(t, obj.SubConfigs(), 1)
	})

	t.Run("Keeps previous state when reload config is invalid", func(t *testing.T) {
		content := `
processor:
  - name: "span_processor/common"
    config:
      drop:
        - match_rules:
            - key: "span_name"
              op: "eq"
              value:
                - "stable"
`
		customContent := `
processor:
  - name: "span_processor/common"
    config:
      replace_value:
        - predicate_key: "span_name"
          rules:
            - filters:
                - key: "span_name"
                  op: "eq"
                  value:
                    - "stable"
              replace_from:
                const_val: "custom"
`

		obj, err := newFactory(
			processor.MustLoadConfigs(content)[0].Config,
			[]processor.SubConfigProcessor{{
				Token:  "token1",
				Type:   define.SubConfigFieldDefault,
				Config: processor.Config{Config: processor.MustLoadConfigs(customContent)[0].Config},
			}},
		)
		assert.NoError(t, err)
		if err != nil {
			return
		}

		oldConfigs := obj.configs
		oldMainConfig := obj.MainConfig()
		oldSubConfigs := obj.SubConfigs()
		oldFetcher := obj.fetcher
		oldGlobal := obj.configs.GetGlobal().(Config)
		oldCustom := obj.configs.GetByToken("token1").(Config)

		obj.Reload(map[string]any{"replace_value": "invalid"}, nil)

		assert.Same(t, oldConfigs, obj.configs)
		assert.Equal(t, oldMainConfig, obj.MainConfig())
		assert.Equal(t, oldSubConfigs, obj.SubConfigs())
		assert.Equal(t, oldFetcher, obj.fetcher)
		assert.Equal(t, oldGlobal, obj.configs.GetGlobal().(Config))
		assert.Equal(t, oldCustom, obj.configs.GetByToken("token1").(Config))
	})
}

func TestProcess(t *testing.T) {
	t.Run("Falls back to global config and applies drop before replace", func(t *testing.T) {
		content := `
processor:
  - name: "span_processor/common"
    config:
      drop:
        - match_rules:
            - key: "span_name"
              op: "eq"
              value:
                - "drop-me"
      replace_value:
        - predicate_key: "span_name"
          rules:
            - filters:
                - key: "attributes.tag"
                  op: "eq"
                  value:
                    - "other"
              replace_from:
                const_val: "global-replaced"
`

		obj, err := NewFactory(processor.MustLoadConfigs(content)[0].Config, nil)
		assert.NoError(t, err)
		if err != nil {
			return
		}

		record := newDropRecord()
		record.Token.Original = "missing-token"

		result, err := obj.Process(record)
		assert.NoError(t, err)
		assert.Nil(t, result)

		traces := record.Data.(ptrace.Traces)
		spans := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans()
		assert.Equal(t, 2, spans.Len())
		assert.Equal(t, "global-replaced", spans.At(0).Name())
		assert.Equal(t, "keep-empty", spans.At(1).Name())
	})

	t.Run("Uses token specific config instead of global config", func(t *testing.T) {
		mainContent := `
processor:
  - name: "span_processor/common"
    config:
      drop:
        - match_rules:
            - key: "span_name"
              op: "eq"
              value:
                - "drop-me"
`
		customContent := `
processor:
  - name: "span_processor/common"
    config:
      replace_value:
        - predicate_key: "span_name"
          rules:
            - filters:
                - key: "span_name"
                  op: "eq"
                  value:
                    - "keep-me"
              replace_from:
                const_val: "token-replaced"
`

		obj, err := NewFactory(
			processor.MustLoadConfigs(mainContent)[0].Config,
			[]processor.SubConfigProcessor{{
				Token: "token1",
				Type:  define.SubConfigFieldDefault,
				Config: processor.Config{
					Config: processor.MustLoadConfigs(customContent)[0].Config,
				},
			}},
		)
		assert.NoError(t, err)
		if err != nil {
			return
		}

		record := newDropRecord()
		record.Token.Original = "token1"

		result, err := obj.Process(record)
		assert.NoError(t, err)
		assert.Nil(t, result)

		traces := record.Data.(ptrace.Traces)
		spans := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans()
		assert.Equal(t, 3, spans.Len())
		assert.Equal(t, "drop-me", spans.At(0).Name())
		assert.Equal(t, "token-replaced", spans.At(1).Name())
		assert.Equal(t, "keep-empty", spans.At(2).Name())
	})

	t.Run("Returns nil nil and leaves record untouched when no actions are configured", func(t *testing.T) {
		obj, err := NewFactory(map[string]any{}, nil)
		assert.NoError(t, err)
		if err != nil {
			return
		}

		record := newReplaceRecord()
		before := firstSpan(record)

		result, err := obj.Process(record)
		assert.NoError(t, err)
		assert.Nil(t, result)
		assert.Equal(t, 1, record.Data.(ptrace.Traces).SpanCount())
		assert.Equal(t, before.Name(), firstSpan(record).Name())

		value, ok := firstSpan(record).Attributes().Get("tag")
		assert.True(t, ok)
		assert.Equal(t, "value", value.AsString())
	})
}

func newTestSpanProcessor() *spanProcessor {
	return &spanProcessor{fetcher: fields.NewSpanFieldFetcher()}
}

func newDropRecord() *define.Record {
	traces := ptrace.NewTraces()
	rs := traces.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().UpsertString("service.name", "svc")
	spans := rs.ScopeSpans().AppendEmpty().Spans()

	span := spans.AppendEmpty()
	span.SetName("drop-me")
	span.Attributes().UpsertString("http.method", "POST")
	span.Attributes().UpsertString("tag", "value")

	span = spans.AppendEmpty()
	span.SetName("keep-me")
	span.Attributes().UpsertString("http.method", "GET")
	span.Attributes().UpsertString("tag", "other")

	span = spans.AppendEmpty()
	span.SetName("keep-empty")

	return &define.Record{RecordType: define.RecordTraces, Data: traces}
}

func newReplaceRecord() *define.Record {
	traces := ptrace.NewTraces()
	rs := traces.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().UpsertString("service.name", "svc")
	rs.Resource().Attributes().UpsertString("cluster", "prod")
	span := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span.SetName("original")
	span.Attributes().UpsertString("tag", "value")
	span.Attributes().UpsertString("empty", "")
	return &define.Record{RecordType: define.RecordTraces, Data: traces}
}

func firstSpan(record *define.Record) ptrace.Span {
	traces := record.Data.(ptrace.Traces)
	return traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
}

func TestDropAction(t *testing.T) {
	p := newTestSpanProcessor()

	t.Run("Drops spans when all match rules match", func(t *testing.T) {
		record := newDropRecord()

		p.dropAction(record, Config{
			Drop: []DropAction{{
				MatchRules: []MatchRule{
					{Key: "span_name", Op: "eq", Value: []string{"drop-me"}},
					{Key: "attributes.http.method", Op: "eq", Value: []string{"POST"}},
				},
			}},
		})

		assert.Equal(t, 2, record.Data.(ptrace.Traces).SpanCount())
	})

	t.Run("Keeps spans when one of multiple rules does not match", func(t *testing.T) {
		record := newDropRecord()

		p.dropAction(record, Config{
			Drop: []DropAction{{
				MatchRules: []MatchRule{
					{Key: "span_name", Op: "eq", Value: []string{"drop-me"}},
					{Key: "attributes.http.method", Op: "eq", Value: []string{"GET"}},
				},
			}},
		})

		assert.Equal(t, 3, record.Data.(ptrace.Traces).SpanCount())
	})

	t.Run("Skips drop rules without match rules", func(t *testing.T) {
		record := newDropRecord()

		p.dropAction(record, Config{
			Drop: []DropAction{{MatchRules: nil}},
		})

		assert.Equal(t, 3, record.Data.(ptrace.Traces).SpanCount())
	})

	t.Run("Ignores non trace records", func(t *testing.T) {
		record := &define.Record{RecordType: define.RecordLogs, Data: "noop"}

		assert.NotPanics(t, func() {
			p.dropAction(record, Config{
				Drop: []DropAction{{
					MatchRules: []MatchRule{{Key: "span_name", Op: "eq", Value: []string{"drop-me"}}},
				}},
			})
		})
	})
}

func TestMatchField(t *testing.T) {
	p := newTestSpanProcessor()

	traces := ptrace.NewTraces()
	rs := traces.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().UpsertString("service.name", "my-service")
	span := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span.SetName("my-span")
	span.Attributes().UpsertString("http.method", "POST")

	t.Run("Matches supported method resource and attribute fields", func(t *testing.T) {
		assert.True(
			t, p.matchField(
				rs.Resource().Attributes(), span, MatchRule{Key: "span_name", Op: "eq", Value: []string{"my-span"}},
			),
		)
		assert.True(
			t, p.matchField(
				rs.Resource().Attributes(),
				span,
				MatchRule{Key: "resource.service.name", Op: "eq", Value: []string{"my-service"}},
			),
		)
		assert.True(
			t, p.matchField(
				rs.Resource().Attributes(),
				span,
				MatchRule{Key: "attributes.http.method", Op: "eq", Value: []string{"POST"}},
			),
		)
	})

	t.Run("Returns true when any value matches with or link", func(t *testing.T) {
		rule := MatchRule{Key: "span_name", Op: "eq", Value: []string{"other-span", "my-span"}, Link: LinkOr}
		assert.True(t, p.matchField(rs.Resource().Attributes(), span, rule))
	})

	t.Run("Returns false when no value matches with or link", func(t *testing.T) {
		rule := MatchRule{Key: "span_name", Op: "eq", Value: []string{"other-span", "another-span"}, Link: LinkOr}
		assert.False(t, p.matchField(rs.Resource().Attributes(), span, rule))
	})

	t.Run("Requires all values to match with default and link", func(t *testing.T) {
		assert.True(
			t, p.matchField(
				rs.Resource().Attributes(),
				span,
				MatchRule{Key: "span_name", Op: "contains", Value: []string{"my", "span"}},
			),
		)
		assert.False(
			t, p.matchField(
				rs.Resource().Attributes(),
				span,
				MatchRule{Key: "span_name", Op: "contains", Value: []string{"my", "other"}},
			),
		)
	})

	t.Run("Returns false for missing or unsupported fields", func(t *testing.T) {
		assert.False(
			t, p.matchField(
				rs.Resource().Attributes(),
				span,
				MatchRule{Key: "resource.cluster", Op: "eq", Value: []string{"prod"}},
			),
		)
		assert.False(
			t, p.matchField(
				rs.Resource().Attributes(),
				span,
				MatchRule{Key: "attributes.http.route", Op: "eq", Value: []string{"/orders"}},
			),
		)
		assert.False(
			t, p.matchField(
				rs.Resource().Attributes(),
				span,
				MatchRule{Key: "", Op: "eq", Value: []string{"value"}},
			),
		)
		assert.False(
			t, p.matchField(
				rs.Resource().Attributes(), span, MatchRule{Key: "unknown.field", Op: "eq", Value: []string{"value"}},
			),
		)
	})
}

func TestReplaceValueAction(t *testing.T) {
	p := newTestSpanProcessor()

	t.Run("Replaces span name from constant when filters match", func(t *testing.T) {
		record := newReplaceRecord()

		p.replaceValueAction(record, Config{
			ReplaceValue: []ReplaceValueAction{{
				PredicateKey: "span_name",
				Rules: []ReplaceRules{{
					Filters: []MatchRule{{Key: "attributes.tag", Op: "eq", Value: []string{"value"}}},
					From:    ReplaceFrom{ConstVal: "replaced"},
				}},
			}},
		})

		assert.Equal(t, "replaced", firstSpan(record).Name())
	})

	t.Run("Uses the first matching rule and ignores later matches", func(t *testing.T) {
		record := newReplaceRecord()

		p.replaceValueAction(record, Config{
			ReplaceValue: []ReplaceValueAction{{
				PredicateKey: "span_name",
				Rules: []ReplaceRules{
					{
						Filters: []MatchRule{{Key: "attributes.tag", Op: "eq", Value: []string{"value"}}},
						From:    ReplaceFrom{ConstVal: "first"},
					},
					{
						Filters: []MatchRule{{Key: "attributes.tag", Op: "eq", Value: []string{"value"}}},
						From:    ReplaceFrom{ConstVal: "second"},
					},
				},
			}},
		})

		assert.Equal(t, "first", firstSpan(record).Name())
	})

	t.Run("Builds replacement values from mixed sources and const fallback", func(t *testing.T) {
		record := newReplaceRecord()

		p.replaceValueAction(record, Config{
			ReplaceValue: []ReplaceValueAction{{
				PredicateKey: "attributes.tag",
				Rules: []ReplaceRules{{
					Filters: []MatchRule{{Key: "span_name", Op: "eq", Value: []string{"original"}}},
					From: ReplaceFrom{
						Source:    []string{"resource.service.name", "attributes.empty", "span_name"},
						ConstVal:  "unknown",
						Separator: "/",
					},
				}},
			}},
		})

		value, ok := firstSpan(record).Attributes().Get("tag")
		assert.True(t, ok)
		assert.Equal(t, "svc/unknown/original", value.AsString())
	})

	t.Run("Skips predicate keys that do not exist and rules without filters", func(t *testing.T) {
		record := newReplaceRecord()

		p.replaceValueAction(record, Config{
			ReplaceValue: []ReplaceValueAction{
				{
					PredicateKey: "attributes.missing",
					Rules: []ReplaceRules{{
						Filters: []MatchRule{{Key: "span_name", Op: "eq", Value: []string{"original"}}},
						From:    ReplaceFrom{ConstVal: "created"},
					}},
				},
				{
					PredicateKey: "span_name",
					Rules: []ReplaceRules{{
						Filters: nil,
						From:    ReplaceFrom{ConstVal: "ignored"},
					}},
				},
			},
		})

		assert.Equal(t, "original", firstSpan(record).Name())
		_, ok := firstSpan(record).Attributes().Get("missing")
		assert.False(t, ok)
	})

	t.Run("Ignores non trace records", func(t *testing.T) {
		record := &define.Record{RecordType: define.RecordLogs, Data: "noop"}
		assert.NotPanics(t, func() {
			p.replaceValueAction(record, Config{
				ReplaceValue: []ReplaceValueAction{{PredicateKey: "span_name"}},
			})
		})
	})
}

func TestCheckSpanField(t *testing.T) {
	p := newTestSpanProcessor()

	traces := ptrace.NewTraces()
	span := traces.ResourceSpans().AppendEmpty().ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span.SetName("my-span")
	span.Attributes().UpsertString("http.method", "POST")

	t.Run("Returns true for existing supported fields", func(t *testing.T) {
		assert.True(t, p.checkSpanField(span, "span_name"))
		assert.True(t, p.checkSpanField(span, "attributes.http.method"))
	})

	t.Run("Returns false for missing or unsupported fields", func(t *testing.T) {
		assert.False(t, p.checkSpanField(span, "attributes.other"))
		assert.False(t, p.checkSpanField(span, "resource.service.name"))
		assert.False(t, p.checkSpanField(span, "unknown.field"))
	})
}

func TestProcessReplaceValue(t *testing.T) {
	p := newTestSpanProcessor()

	record := newReplaceRecord()
	traces := record.Data.(ptrace.Traces)
	rs := traces.ResourceSpans().At(0)
	span := rs.ScopeSpans().At(0).Spans().At(0)

	t.Run("Returns constant value when source is empty", func(t *testing.T) {
		cfg := ReplaceFrom{ConstVal: "fixed"}
		assert.Equal(t, "fixed", p.processReplaceValue(rs.Resource().Attributes(), span, cfg))
	})

	t.Run("Joins method resource and attribute values", func(t *testing.T) {
		cfg := ReplaceFrom{
			Source:    []string{"span_name", "resource.cluster", "attributes.tag"},
			Separator: ":",
		}
		assert.Equal(t, "original:prod:value", p.processReplaceValue(rs.Resource().Attributes(), span, cfg))
	})

	t.Run("Uses const fallback for missing and empty values", func(t *testing.T) {
		cfg := ReplaceFrom{
			Source:    []string{"attributes.empty", "attributes.missing", "resource.missing"},
			ConstVal:  "unknown",
			Separator: "/",
		}
		assert.Equal(t, "unknown/unknown/unknown", p.processReplaceValue(rs.Resource().Attributes(), span, cfg))
	})
}

func TestUpdateFieldValue(t *testing.T) {
	p := newTestSpanProcessor()

	t.Run("Updates span name and attributes", func(t *testing.T) {
		traces := ptrace.NewTraces()
		span := traces.ResourceSpans().AppendEmpty().ScopeSpans().AppendEmpty().Spans().AppendEmpty()
		span.SetName("old")
		span.Attributes().UpsertString("a1", "old_val")

		p.updateFieldValue(span, map[string]string{
			"span_name":     "new",
			"attributes.a1": "new_val",
			"attributes.a2": "created",
		})

		assert.Equal(t, "new", span.Name())
		v1, ok := span.Attributes().Get("a1")
		assert.True(t, ok)
		assert.Equal(t, "new_val", v1.AsString())
		v2, ok := span.Attributes().Get("a2")
		assert.True(t, ok)
		assert.Equal(t, "created", v2.AsString())
	})

	t.Run("Ignores unsupported update fields", func(t *testing.T) {
		traces := ptrace.NewTraces()
		span := traces.ResourceSpans().AppendEmpty().ScopeSpans().AppendEmpty().Spans().AppendEmpty()
		span.SetName("old")

		p.updateFieldValue(span, map[string]string{
			"trace_id":              "new-trace-id",
			"resource.service.name": "new-service",
		})

		assert.Equal(t, "old", span.Name())
		_, ok := span.Attributes().Get("service.name")
		assert.False(t, ok)
	})
}
