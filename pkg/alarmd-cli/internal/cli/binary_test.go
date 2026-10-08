package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBinaryLoginInvokeRenewalAndLogout(t *testing.T) {
	name := "alarmd-cli"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	build := exec.Command("go", "build", "-buildvcs=false", "-o", binary, "../..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	for _, tls := range []bool{false, true} {
		t.Run(fmtTransport(tls), func(t *testing.T) {
			s := &pairingServer{t: t, partialInvoke: true}
			var server *httptest.Server
			if tls {
				server = httptest.NewTLSServer(s)
			} else {
				server = httptest.NewServer(s)
			}
			defer server.Close()
			s.p = fixtureProfile(server.URL)
			dir := filepath.Join(t.TempDir(), "配置 space")
			command := func(input string, args ...string) *exec.Cmd {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				t.Cleanup(cancel)
				cmd := exec.CommandContext(ctx, binary, args...)
				cmd.Env = append(os.Environ(), "ALARMD_CLI_CONFIG_DIR="+dir)
				cmd.Stdin = strings.NewReader(input)
				return cmd
			}
			runBinary := func(want int, input string, args ...string) map[string]any {
				t.Helper()
				cmd := command(input, args...)
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				out, err := cmd.Output()
				code := 0
				if err != nil {
					if exit, ok := err.(*exec.ExitError); ok {
						code = exit.ExitCode()
					} else {
						t.Fatal(err)
					}
				}
				if code != want {
					t.Fatalf("%v: exit=%d want=%d stdout=%s stderr=%s", args, code, want, out, stderr.String())
				}
				for _, secret := range []string{testToken, testGrant, testRefresh} {
					if bytes.Contains(out, []byte(secret)) || strings.Contains(stderr.String(), secret) {
						t.Fatal("binary leaked credentials")
					}
				}
				var result map[string]any
				if err := json.Unmarshal(out, &result); err != nil {
					t.Fatalf("invalid stdout JSON: %s", out)
				}
				return result
			}
			args := []string{"auth", "login"}
			if tls {
				ca := filepath.Join(t.TempDir(), "私有 CA.pem")
				if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--ca-cert", ca)
			}
			runBinary(0, bundle(s.p)+"\r\n", args...)
			runBinary(0, "", "profile", "list")
			input := filepath.Join(t.TempDir(), "参数 space.json")
			if err := os.WriteFile(input, []byte(`{"records":2}`), 0600); err != nil {
				t.Fatal(err)
			}
			result := runBinary(3, "", "invoke", "fixture.operation", "--env", s.p.EnvironmentID, "--input", "@"+input)
			path := stringField(objectField(result, "meta"), "result_file")
			data, err := os.ReadFile(path)
			if err != nil || !filepath.IsAbs(path) || !json.Valid(data) {
				t.Fatalf("invalid evidence: %v", err)
			}
			assertPrivatePath(t, path, false)
			store := Store{Dir: dir}
			p, err := store.get(s.p.EnvironmentID)
			if err != nil {
				t.Fatal(err)
			}
			p.ExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
			if err := store.save(p, false); err != nil {
				t.Fatal(err)
			}
			// Both real processes begin from the same expired on-disk session.
			commands := []*exec.Cmd{command("", "auth", "status", "--env", p.EnvironmentID), command("", "auth", "status", "--env", p.EnvironmentID)}
			for _, cmd := range commands {
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
			}
			for _, cmd := range commands {
				if err := cmd.Wait(); err != nil {
					t.Fatal(err)
				}
			}
			s.mu.Lock()
			count := len(s.refreshes)
			s.mu.Unlock()
			if count != 1 {
				t.Fatalf("concurrent processes renewed %d times", count)
			}
			p, err = store.get(p.EnvironmentID)
			if err != nil || p.RefreshToken != testRefresh+"-next" {
				t.Fatalf("next credential not committed: %v", err)
			}
			runBinary(0, "", "auth", "logout", "--env", p.EnvironmentID)
			runBinary(1, "", "discover", "--env", p.EnvironmentID)
			runBinary(2, "", "invoke", "fixture.operation")
			assertPrivatePath(t, filepath.Join(dir, "profiles.json"), false)
		})
	}
}

func fmtTransport(tls bool) string {
	if tls {
		return "https-private-ca"
	}
	return "http"
}
