package aegisv2

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
)

type fatalSessionBuilder struct{}

func (fatalSessionBuilder) SupportedTypes() []EventType {
	return []EventType{EventTypeSession}
}

func (fatalSessionBuilder) Build(_ *BuildContext) error {
	return NewFatalBuildError(errors.New("forced fatal"))
}

type recoverableSessionBuilder struct{}

func (recoverableSessionBuilder) SupportedTypes() []EventType {
	return []EventType{EventTypeSession}
}

func (recoverableSessionBuilder) Build(_ *BuildContext) error {
	return NewRecoverableBuildError(errors.New("forced recoverable"))
}

type plainSessionBuilderError struct{}

func (plainSessionBuilderError) SupportedTypes() []EventType {
	return []EventType{EventTypeSession}
}

func (plainSessionBuilderError) Build(_ *BuildContext) error {
	return errors.New("forced plain error")
}

type noOpBuilder struct {
	types []EventType
}

func (b *noOpBuilder) SupportedTypes() []EventType {
	return b.types
}

func (*noOpBuilder) Build(_ *BuildContext) error {
	return nil
}

func TestBuilderRegistry_DefaultCoversKnownEventTypes(t *testing.T) {
	r := newDefaultBuilderRegistry()
	knownTypes := []EventType{
		EventTypeAPI,
		EventTypeAssetSpeed,
		EventTypeCustom,
		EventTypePagePerformance,
		EventTypePV,
		EventTypeSession,
		EventTypeWebsocket,
		EventTypeError,
		EventTypeWebVitals,
	}

	for _, typ := range knownTypes {
		builder, ok := r.Get(typ)
		assert.True(t, ok, "event type %s should have default builder", typ)
		assert.NotNil(t, builder)
	}

	_, ok := r.Get(EventTypeUnknown)
	assert.False(t, ok)
}

func TestBuilderRegistry_RegisterOverridesExistingType(t *testing.T) {
	r := newDefaultBuilderRegistry()

	custom := &noOpBuilder{types: []EventType{EventTypeAPI}}
	require.NoError(t, r.Register(custom))

	builder, ok := r.Get(EventTypeAPI)
	require.True(t, ok)
	assert.Same(t, custom, builder)
}

func TestBuilderRegistry_RegisterNilBuilderReturnsError(t *testing.T) {
	r := NewBuilderRegistry()

	var builder *noOpBuilder
	err := r.Register(builder)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil span builder")
}

func TestSplitTraces_UnknownTypeDegradesAndCounts(t *testing.T) {
	oldRegistry := defaultBuilderRegistry
	defaultBuilderRegistry = newDefaultBuilderRegistry()
	resetBuilderStatsForTest()
	defer func() {
		defaultBuilderRegistry = oldRegistry
		resetBuilderStatsForTest()
	}()

	payload := collectPayload{Bean: clientBean{Version: "1.0.0"}}
	records := []d2Record{{
		Fields: d2Fields{Type: "custom", Session: sessionInfo{ID: "session-1"}},
		Message: []d2Message{{
			Msg:       "custom.event",
			Timestamp: 1780992434065,
			raw:       map[string]any{"foo": "bar"},
		}},
	}}

	traces, err := splitTraces(payload, records, pcommon.TraceID{})
	require.NoError(t, err)

	spans := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans()
	require.Equal(t, 1, spans.Len())
	span := spans.At(0)
	assert.Equal(t, "custom", span.Name())
	assert.Equal(t, 1, span.Events().Len())
	assert.Equal(t, spanEventAegisFallback, span.Events().At(0).Name())

	degradeCount, unknownTypeCount := builderStatsSnapshot()
	assert.EqualValues(t, 1, degradeCount)
	assert.EqualValues(t, 1, unknownTypeCount)
}

func TestSplitTraces_EmptyMessageDropsMessageDrivenSpanButKeepsAction(t *testing.T) {
	resetBuilderStatsForTest()
	defer resetBuilderStatsForTest()

	payload := collectPayload{Topic: "SDK-xxxxx", Bean: clientBean{Version: "1.0.0"}}
	records := []d2Record{{
		Fields: d2Fields{
			Type:    "api",
			Plugin:  "api",
			Session: sessionInfo{ID: "session-1"},
			Action: actionInfo{
				ID:         "action-1",
				Timestamp:  1780994775565,
				ActionType: "click",
				ActionName: "open-api",
			},
		},
	}}

	traces, err := splitTraces(payload, records, pcommon.TraceID{})
	require.NoError(t, err)

	spans := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans()
	require.Equal(t, 1, spans.Len())
	assert.Equal(t, "action.click", spans.At(0).Name())
	assert.EqualValues(t, 1, emptyMessageDropCountSnapshot())

	degradeCount, unknownTypeCount := builderStatsSnapshot()
	assert.EqualValues(t, 0, degradeCount)
	assert.EqualValues(t, 0, unknownTypeCount)
}

func TestSplitTraces_RecoverableBuilderErrorDegradesAndContinues(t *testing.T) {
	oldRegistry := defaultBuilderRegistry
	r := NewBuilderRegistry()
	require.NoError(t, r.Register(recoverableSessionBuilder{}))
	defaultBuilderRegistry = r
	resetBuilderStatsForTest()
	defer func() {
		defaultBuilderRegistry = oldRegistry
		resetBuilderStatsForTest()
	}()

	payload := collectPayload{Bean: clientBean{Version: "1.0.0"}}
	records := []d2Record{{
		Fields: d2Fields{Type: "session", Plugin: "session", Session: sessionInfo{ID: "session-1"}},
		Message: []d2Message{{
			Msg:       "session",
			Timestamp: 1780992433875,
			raw:       map[string]any{},
		}},
	}}

	traces, err := splitTraces(payload, records, pcommon.TraceID{})
	require.NoError(t, err)

	spans := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans()
	require.Equal(t, 1, spans.Len())
	assert.Equal(t, "session", spans.At(0).Name())

	degradeCount, unknownTypeCount := builderStatsSnapshot()
	assert.EqualValues(t, 1, degradeCount)
	assert.EqualValues(t, 0, unknownTypeCount)
}

func TestSplitTraces_PlainBuilderErrorReturnsError(t *testing.T) {
	oldRegistry := defaultBuilderRegistry
	r := NewBuilderRegistry()
	require.NoError(t, r.Register(plainSessionBuilderError{}))
	defaultBuilderRegistry = r
	resetBuilderStatsForTest()
	defer func() {
		defaultBuilderRegistry = oldRegistry
		resetBuilderStatsForTest()
	}()

	payload := collectPayload{Bean: clientBean{Version: "1.0.0"}}
	records := []d2Record{{
		Fields: d2Fields{Type: "session", Plugin: "session", Session: sessionInfo{ID: "session-1"}},
		Message: []d2Message{{
			Msg:       "session",
			Timestamp: 1780992433875,
			raw:       map[string]any{},
		}},
	}}

	_, err := splitTraces(payload, records, pcommon.TraceID{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "forced plain error")

	degradeCount, unknownTypeCount := builderStatsSnapshot()
	assert.EqualValues(t, 0, degradeCount)
	assert.EqualValues(t, 0, unknownTypeCount)
}

func TestSplitTraces_FatalBuilderErrorReturnsError(t *testing.T) {
	oldRegistry := defaultBuilderRegistry
	r := NewBuilderRegistry()
	require.NoError(t, r.Register(fatalSessionBuilder{}))
	defaultBuilderRegistry = r
	resetBuilderStatsForTest()
	defer func() {
		defaultBuilderRegistry = oldRegistry
		resetBuilderStatsForTest()
	}()

	payload := collectPayload{Bean: clientBean{Version: "1.0.0"}}
	records := []d2Record{{
		Fields: d2Fields{Type: "session", Plugin: "session", Session: sessionInfo{ID: "session-1"}},
		Message: []d2Message{{
			Msg:       "session",
			Timestamp: 1780992433875,
			raw:       map[string]any{},
		}},
	}}

	_, err := splitTraces(payload, records, pcommon.TraceID{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fatal")

	degradeCount, unknownTypeCount := builderStatsSnapshot()
	assert.EqualValues(t, 0, degradeCount)
	assert.EqualValues(t, 0, unknownTypeCount)
}
