package platformsettings

import (
	"strings"
	"testing"
)

// Every field the package reads must have a home in the publisher's tree. A
// field added to Fields without a DBKey case would subscribe to
// "<prefix>:<tenant>:" and read nothing, without any error saying so; the
// closed switch has to be closed over the same list the reader iterates.
func TestEveryFieldHasADomainKeyThatEndsWithItsName(t *testing.T) {
	seen := map[string]Field{}
	for _, field := range Fields {
		key := field.DBKey()
		if key == "" {
			t.Fatalf("field %q has no DB key: it would be subscribed under an empty key and never read", field)
		}
		if !strings.HasPrefix(key, "base_config.") || !strings.HasSuffix(key, "."+string(field)) {
			t.Fatalf("field %q DB key %q is not base_config.<domain>.<field>", field, key)
		}
		if other, dup := seen[key]; dup {
			t.Fatalf("fields %q and %q share DB key %q", other, field, key)
		}
		seen[key] = field
	}
	if got := Field("not_a_field").DBKey(); got != "" {
		t.Fatalf("an unknown field must have no key, got %q", got)
	}
}
