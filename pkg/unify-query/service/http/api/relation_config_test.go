package api

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/eventbus"
)

// 全量重载配置的测试使用独立 Viper；清理时恢复配置来源和运行值。
// 这些进程全局配置用例必须串行执行。
func useRelationTestConfig(t *testing.T) {
	t.Helper()
	previousViper := *viper.GetViper()
	previousRuntime := relationRuntimeConfig()
	previousSettings := viper.AllSettings()
	*viper.GetViper() = *viper.New()
	t.Cleanup(func() {
		for key, value := range previousRuntime {
			viper.Set(key, value)
		}
		v1beta3.LoadConfig()
		*viper.GetViper() = previousViper
	})
	eventbus.EventBus.Publish(eventbus.EventSignalConfigPreParse)
	defaults := make(map[string]any, len(previousRuntime))
	for key := range previousRuntime {
		defaults[key] = viper.Get(key)
	}
	// 后端地址等其他模块配置仍需在测试中可读，不能被关系配置隔离清空。
	require.NoError(t, viper.MergeConfigMap(previousSettings))
	for key, value := range defaults {
		viper.Set(key, value)
	}
	v1beta3.LoadConfig()
}

func TestRelationFailureResponseRestoresConfig(t *testing.T) {
	before := relationRuntimeConfig()
	beforeSettings := viper.AllSettings()
	require.True(t, t.Run("failure_responses", TestRelationFailureResponseCompatibility))
	require.Equal(t, before, relationRuntimeConfig())
	require.Equal(t, beforeSettings, viper.AllSettings())
}

func TestRelationConfigRestoresOverridesAndRuntime(t *testing.T) {
	useRelationTestConfig(t)
	viper.Set(v1beta3.MaxAllowedHopsConfigPath, 7)
	viper.Set(v1beta3.YoloModeConfigPath, true)
	viper.Set("review.unrelated", "preserved")
	v1beta3.LoadConfig()
	// 模块运行值可以与 Viper 配置不同，两者均需独立恢复。
	v1beta3.DefaultLimit = 37
	before := relationRuntimeConfig()
	beforeSettings := viper.AllSettings()
	require.True(t, t.Run("isolated_defaults", func(t *testing.T) {
		useRelationTestConfig(t)
		require.Equal(t, 5, v1beta3.MaxAllowedHops)
		require.Equal(t, 100, v1beta3.DefaultLimit)
		require.False(t, v1beta3.YoloModeEnabled())
		require.Equal(t, "preserved", viper.GetString("review.unrelated"))
		for key, value := range relationRuntimeConfig() {
			switch value.(type) {
			case bool:
				viper.Set(key, false)
			default:
				viper.Set(key, 1)
			}
		}
		v1beta3.LoadConfig()
	}))
	require.Equal(t, before, relationRuntimeConfig())
	require.Equal(t, beforeSettings, viper.AllSettings())
}

func relationRuntimeConfig() map[string]any {
	return map[string]any{
		v1beta3.MaxHopsConfigPath:                          v1beta3.DefaultMaxHops,
		v1beta3.MaxAllowedHopsConfigPath:                   v1beta3.MaxAllowedHops,
		v1beta3.DefaultLimitConfigPath:                     v1beta3.DefaultLimit,
		v1beta3.MaxRangePointsConfigPath:                   v1beta3.MaxRangePoints,
		v1beta3.MaxTargetsConfigPath:                       v1beta3.MaxTargets,
		v1beta3.MaxGraphNodesConfigPath:                    v1beta3.MaxGraphNodes,
		v1beta3.MaxGraphEdgesConfigPath:                    v1beta3.MaxGraphEdges,
		v1beta3.MaxGraphResultsConfigPath:                  v1beta3.MaxGraphResults,
		v1beta3.MaxGraphNodeInfosConfigPath:                v1beta3.MaxGraphNodeInfos,
		v1beta3.MaxSharedTopologyPointsConfigPath:          v1beta3.MaxSharedTopologyPoints,
		v1beta3.MaxSharedTopologyBackendBytesConfigPath:    v1beta3.MaxSharedTopologyBackendBytes,
		v1beta3.MaxSharedTopologyMatrixPointsConfigPath:    v1beta3.MaxSharedTopologyMatrixPoints,
		v1beta3.MaxSharedTopologyOutputElementsConfigPath:  v1beta3.MaxSharedTopologyOutputElements,
		v1beta3.MaxSharedTopologyOutputBytesConfigPath:     v1beta3.MaxSharedTopologyOutputBytes,
		v1beta3.MaxSharedTopologyRequestBytesConfigPath:    v1beta3.MaxSharedTopologyRequestBytes,
		v1beta3.MaxSharedTopologyQueriesConfigPath:         v1beta3.MaxSharedTopologyQueries,
		v1beta3.YoloModeConfigPath:                         v1beta3.YoloModeEnabled(),
		v1beta3.SharedTopologyReuseMatrixConfigPath:        v1beta3.SharedTopologyReuseMatrix,
		v1beta3.SharedTopologyPlanCandidatesConfigPath:     v1beta3.SharedTopologyPlanCandidates,
		v1beta3.MaxSharedTopologyConcurrentConfigPath:      v1beta3.MaxSharedTopologyConcurrent,
		v1beta3.MaxSharedTopologyReservedBytesConfigPath:   v1beta3.MaxSharedTopologyReservedBytes,
		v1beta3.SharedTopologyRequestMemoryBytesConfigPath: v1beta3.SharedTopologyRequestMemoryBytes,
		v1beta3.SharedTopologyMemoryHeadroomConfigPath:     v1beta3.SharedTopologyMemoryHeadroom,
		v1beta3.DefaultLookBackDeltaConfigPath:             v1beta3.DefaultLookBackDelta,
	}
}
