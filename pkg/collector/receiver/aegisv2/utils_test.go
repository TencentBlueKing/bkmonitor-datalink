package aegisv2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
)

func TestUpsertFlattenedMap_DefaultBehavior(t *testing.T) {
	attrs := pcommon.NewMap()
	objs := map[string]any{
		"a": float64(1),
		"b": "x",
	}

	upsertFlattenedMap(attrs, "p", objs)

	v, ok := attrs.Get("p.a")
	require.True(t, ok)
	assert.EqualValues(t, 1, v.IntVal())
	v, ok = attrs.Get("p.b")
	require.True(t, ok)
	assert.Equal(t, "x", v.StringVal())
}

func TestUpsertFlattenedMapWithAllowlist_FiltersKeys(t *testing.T) {
	attrs := pcommon.NewMap()
	objs := map[string]any{
		"a": float64(1),
		"b": "x",
		"c": true,
	}
	allowlist := map[string]struct{}{
		"a": {},
		"c": {},
	}

	upsertFlattenedMapWithAllowlist(attrs, "p", objs, allowlist)

	_, ok := attrs.Get("p.a")
	assert.True(t, ok)
	_, ok = attrs.Get("p.c")
	assert.True(t, ok)
	_, ok = attrs.Get("p.b")
	assert.False(t, ok)
}

func TestUpsertFlattenedMapWithAllowlist_EmptyAllowlistBehavesAsDefault(t *testing.T) {
	attrs := pcommon.NewMap()
	objs := map[string]any{
		"a": float64(1),
		"b": "x",
	}

	upsertFlattenedMapWithAllowlist(attrs, "p", objs, map[string]struct{}{})

	_, ok := attrs.Get("p.a")
	assert.True(t, ok)
	_, ok = attrs.Get("p.b")
	assert.True(t, ok)
}

func FuzzD2MessageUnmarshalJSON(f *testing.F) {
	seeds := []string{
		`{"k":"v","n":1}`,
		`"{\"k\":\"v\",\"n\":1}"`,
		`null`,
		``,
		`""`,
		`[]`,
		`{`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		var msg d2Message
		_ = msg.UnmarshalJSON([]byte(input))
	})
}
