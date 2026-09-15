package comparator

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestComparePythonDedupeCaptureGolden(t *testing.T) {
	payload, err := os.ReadFile("../contract/testdata/python-event-dedupe.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name     string          `json:"name"`
		Event    json.RawMessage `json:"event"`
		Identity json.RawMessage `json:"identity"`
	}
	if err := json.Unmarshal(payload, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("empty Python oracle")
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var identity struct {
				MD5 string `json:"dedupe_md5"`
			}
			if err := json.Unmarshal(c.Identity, &identity); err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(c.Event)
			row := map[string]any{
				"raw_base64":      base64.StdEncoding.EncodeToString(c.Event),
				"raw_sha256":      hex.EncodeToString(hash[:]),
				"dedupe_identity": c.Identity,
			}
			encode := func() []byte {
				b, err := json.Marshal(row)
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
			line := encode()
			if err := ComparePythonDedupeCapture(line, identity.MD5, len(line)); err != nil {
				t.Fatal(err)
			}
			if err := ComparePythonDedupeCapture(line, "different-native-identity", len(line)); err == nil {
				t.Fatal("accepted different native fingerprint")
			}
			if err := ComparePythonDedupeCapture(line, identity.MD5, len(line)-1); err == nil {
				t.Fatal("ignored capture limit")
			}
			row["raw_sha256"] = "altered"
			if err := ComparePythonDedupeCapture(encode(), identity.MD5, 1<<20); err == nil {
				t.Fatal("accepted altered raw message")
			}
			row["raw_sha256"] = hex.EncodeToString(hash[:])
			var mutated map[string]json.RawMessage
			if err := json.Unmarshal(c.Identity, &mutated); err != nil {
				t.Fatal(err)
			}
			mutated["dedupe_values"] = json.RawMessage(`[]`)
			row["dedupe_identity"] = mutated
			if err := ComparePythonDedupeCapture(encode(), identity.MD5, 1<<20); err == nil {
				t.Fatal("accepted altered Python values")
			}
		})
	}
}
