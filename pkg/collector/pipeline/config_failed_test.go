// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pipeline

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/metrictest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
)

const (
	configFailedName  = "bk_collector_processor_config_failed_total"
	configFailingName = "bk_collector_processor_config_failing"
	unbuiltName       = "bk_collector_pipeline_unbuilt"
)

func configFailed(t *testing.T, kind, config string) float64 {
	t.Helper()
	v, _ := metrictest.Value(t, configFailedName, map[string]string{"kind": kind, "config": config})
	return v
}

// failing reads the state of a kind's config, and whether its cell exists.
func failing(t *testing.T, kind, config string) (float64, bool) {
	t.Helper()
	return metrictest.Value(t, configFailingName, map[string]string{"kind": kind, "config": config})
}

func unbuilt(t *testing.T, rtype string) (float64, bool) {
	t.Helper()
	return metrictest.Value(t, unbuiltName, map[string]string{"record_type": rtype})
}

// apdexConfig is a main configuration with one apdex calculator and the
// traces pipeline that names it, reading sub-configurations from pattern.
func apdexConfig(calculator string, pattern string) string {
	list := "[]"
	if pattern != "" {
		list = `["` + pattern + `"]`
	}
	return `
apm:
    patterns: ` + list + `
processor:
    - name: "apdex_calculator/fixed"
      config:
        calculator: ` + calculator + `
pipeline:
    - name: "traces_pipeline/common"
      type: "traces"
      processors:
        - "apdex_calculator/fixed"
`
}

const (
	goodCalculator = `{type: "fixed", apdex_status: "satisfied"}`
	badCalculator  = `"not a calculator"`
)

var (
	goodApdex = apdexConfig(goodCalculator, "")
	badApdex  = apdexConfig(badCalculator, "")
	// noPipelines builds its processors and then fails, as it has no
	// pipeline section, so it never takes effect.
	noPipelines = `
apm:
    patterns: []
processor:
    - name: "apdex_calculator/fixed"
      config:
        calculator: ` + goodCalculator + `
`
)

// Once the manager is built, every processor this binary registers (the
// ones this test imports) has both of its cells, and unknown its main cell,
// in the counter and in the state; every record type has its cell of
// unbuilt pipelines.
func TestProcessorConfigFailedCellsExistOnceTheManagerIsBuilt(t *testing.T) {
	_, err := New(confengine.MustLoadConfigContent(goodApdex))
	require.NoError(t, err)

	var want []string
	for _, name := range []string{
		"apdex_calculator", "attribute_filter", "metrics_filter", "rate_limiter", "resource_filter",
		"sampler", "service_discover", "token_checker", "traces_deriver",
	} {
		want = append(want, "config=main,kind="+name, "config=sub,kind="+name)
	}
	want = append(want, "config=main,kind=unknown")
	sort.Strings(want)
	assert.Equal(t, want, metrictest.Series(t, configFailedName))
	assert.Equal(t, want, metrictest.Series(t, configFailingName))

	var types []string
	for _, rtype := range []string{
		"undefined", "traces", "rumTraces", "profiles", "metrics", "logs", "traces.derived", "metrics.derived",
		"logs.derived", "pushgateway", "fta", "remotewrite", "proxy", "pingserver", "beat", "tars", "logpush",
	} {
		types = append(types, "record_type="+rtype)
	}
	sort.Strings(types)
	assert.Equal(t, types, metrictest.Series(t, unbuiltName))
}

// A processor whose own configuration does not build is counted, at the
// start and on a reload, and the pipeline that needs it is left out until a
// configuration builds it again. A configuration that builds is not counted.
func TestAProcessorWhoseConfigDoesNotBuildIsCounted(t *testing.T) {
	before := configFailed(t, define.ProcessorApdexCalculator, "main")

	_, err := New(confengine.MustLoadConfigContent(badApdex))
	require.NoError(t, err)
	assert.Equal(t, before+1, configFailed(t, define.ProcessorApdexCalculator, "main"))

	mgr, err := New(confengine.MustLoadConfigContent(goodApdex))
	require.NoError(t, err)
	require.NotNil(t, mgr.GetPipeline(define.RecordTraces))
	assert.Equal(t, before+1, configFailed(t, define.ProcessorApdexCalculator, "main"))

	require.NoError(t, mgr.Reload(confengine.MustLoadConfigContent(badApdex)))
	assert.Equal(t, before+2, configFailed(t, define.ProcessorApdexCalculator, "main"))
	assert.Nil(t, mgr.GetPipeline(define.RecordTraces))

	require.NoError(t, mgr.Reload(confengine.MustLoadConfigContent(goodApdex)))
	assert.Equal(t, before+2, configFailed(t, define.ProcessorApdexCalculator, "main"))
	assert.NotNil(t, mgr.GetPipeline(define.RecordTraces))
}

// The state holds what fails in the configuration now in effect. A
// configuration that does not build sets it, and keeps it set however many
// times it is reloaded, while the counter goes up on each build; a reload
// that fails before it takes effect leaves it as it was, the broken
// configuration still being the one in effect; a configuration that builds
// brings it back to 0.
func TestTheFailingStateFollowsTheConfigurationInEffect(t *testing.T) {
	state := func() [2]float64 {
		t.Helper()
		main, ok := failing(t, define.ProcessorApdexCalculator, "main")
		require.True(t, ok)
		pipelines, ok := unbuilt(t, "traces")
		require.True(t, ok)
		return [2]float64{main, pipelines}
	}
	mgr, err := New(confengine.MustLoadConfigContent(goodApdex))
	require.NoError(t, err)
	assert.Equal(t, [2]float64{0, 0}, state())
	counted := configFailed(t, define.ProcessorApdexCalculator, "main")

	for i := 1; i <= 2; i++ {
		require.NoError(t, mgr.Reload(confengine.MustLoadConfigContent(badApdex)))
		assert.Equal(t, [2]float64{1, 1}, state(), "after bad reload %d", i)
		assert.Equal(t, counted+float64(i), configFailed(t, define.ProcessorApdexCalculator, "main"))
	}

	require.Error(t, mgr.Reload(confengine.MustLoadConfigContent(noPipelines)))
	assert.Equal(t, [2]float64{1, 1}, state(), "after a reload that did not take effect")

	require.NoError(t, mgr.Reload(confengine.MustLoadConfigContent(goodApdex)))
	assert.Equal(t, [2]float64{0, 0}, state(), "after a good reload")
}

// A configured processor whose name this build does not register is counted
// as unknown, main, at the start and on a reload, held in the state, and the
// pipeline that names it is left out; no registered kind's cell moves and
// the name does not become a label.
func TestAProcessorNameThisBuildDoesNotRegisterIsCountedAsUnknown(t *testing.T) {
	content := `
apm:
    patterns: []
processor:
    - name: "no_such_processor/fixed"
      config:
pipeline:
    - name: "traces_pipeline/common"
      type: "traces"
      processors:
        - "no_such_processor/fixed"
`
	before := configFailed(t, "unknown", "main")
	apdex := configFailed(t, define.ProcessorApdexCalculator, "main")

	mgr, err := New(confengine.MustLoadConfigContent(content))
	require.NoError(t, err)
	assert.Equal(t, before+1, configFailed(t, "unknown", "main"))
	assert.Nil(t, mgr.GetPipeline(define.RecordTraces))

	require.NoError(t, mgr.Reload(confengine.MustLoadConfigContent(content)))
	assert.Equal(t, before+2, configFailed(t, "unknown", "main"))
	assert.Equal(t, apdex, configFailed(t, define.ProcessorApdexCalculator, "main"))
	state, ok := failing(t, "unknown", "main")
	assert.True(t, ok)
	assert.Equal(t, float64(1), state)
	for _, series := range append(metrictest.Series(t, configFailedName), metrictest.Series(t, configFailingName)...) {
		assert.NotContains(t, series, "no_such_processor")
	}
}

// subConfigFiles writes one sub-configuration file per token, each giving
// the apdex processor its calculator, and returns the pattern that reads them.
func subConfigFiles(t *testing.T, calculators map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	i := 0
	for token, calculator := range calculators {
		i++
		content := `
type: "subconfig"
token: "` + token + `"
default:
  processor:
    - name: "apdex_calculator/fixed"
      config:
        calculator: ` + calculator + `
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "sub"+strconv.Itoa(i)+".yml"), []byte(content), 0o600))
	}
	return filepath.Join(dir, "*.yml")
}

// A sub-configuration that does not decode is skipped and counted on every
// build, and a reload builds the processor twice; the state holds how many
// the instance in effect skipped, not how many builds there were, and goes
// back to 0 once the sub-configurations decode.
func TestTheSkippedSubConfigStateCountsTheInstanceInEffect(t *testing.T) {
	sub := func() float64 {
		t.Helper()
		v, ok := failing(t, define.ProcessorApdexCalculator, "sub")
		require.True(t, ok)
		return v
	}
	counted := configFailed(t, define.ProcessorApdexCalculator, "sub")
	oneBad := apdexConfig(goodCalculator, subConfigFiles(t, map[string]string{"good-app": goodCalculator, "bad-app": badCalculator}))

	mgr, err := New(confengine.MustLoadConfigContent(oneBad))
	require.NoError(t, err)
	require.NotNil(t, mgr.GetPipeline(define.RecordTraces))
	assert.Equal(t, float64(1), sub())
	assert.Equal(t, counted+1, configFailed(t, define.ProcessorApdexCalculator, "sub"))

	require.NoError(t, mgr.Reload(confengine.MustLoadConfigContent(oneBad)))
	assert.Equal(t, float64(1), sub())
	assert.Equal(t, counted+3, configFailed(t, define.ProcessorApdexCalculator, "sub"))

	twoBad := apdexConfig(goodCalculator, subConfigFiles(t, map[string]string{"bad-app": badCalculator, "worse-app": badCalculator}))
	require.NoError(t, mgr.Reload(confengine.MustLoadConfigContent(twoBad)))
	assert.Equal(t, float64(2), sub())

	allGood := apdexConfig(goodCalculator, subConfigFiles(t, map[string]string{"good-app": goodCalculator}))
	require.NoError(t, mgr.Reload(confengine.MustLoadConfigContent(allGood)))
	assert.Equal(t, float64(0), sub())
}

// An application's sub-configuration that does not decode is skipped and
// counted each time the processor is built or reloaded with it; the
// processor still builds, and a sub-configuration that decodes is not counted.
func TestASubConfigThatDoesNotDecodeIsCounted(t *testing.T) {
	main := map[string]any{"calculator": map[string]any{"type": "fixed", "apdex_status": "satisfied"}}
	sub := func(token string, calculator any) processor.SubConfigProcessor {
		return processor.SubConfigProcessor{
			Token: token,
			Type:  define.SubConfigFieldDefault,
			Config: processor.Config{
				Name:   "apdex_calculator/fixed",
				Config: map[string]any{"calculator": calculator},
			},
		}
	}
	create := processor.GetProcessorCreator("apdex_calculator/fixed")
	require.NotNil(t, create)

	before := configFailed(t, define.ProcessorApdexCalculator, "sub")
	p, err := create(main, []processor.SubConfigProcessor{sub("good-app", map[string]any{"type": "fixed", "apdex_status": "tolerating"})})
	require.NoError(t, err)
	assert.Equal(t, before, configFailed(t, define.ProcessorApdexCalculator, "sub"))

	subs := []processor.SubConfigProcessor{
		sub("good-app", map[string]any{"type": "fixed", "apdex_status": "tolerating"}),
		sub("bad-app", "not a calculator"),
	}
	_, err = create(main, subs)
	require.NoError(t, err)
	assert.Equal(t, before+1, configFailed(t, define.ProcessorApdexCalculator, "sub"))

	p.Reload(main, subs)
	assert.Equal(t, before+2, configFailed(t, define.ProcessorApdexCalculator, "sub"))
}
