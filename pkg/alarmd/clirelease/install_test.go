// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package clirelease

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// installerHost serves a release under a path prefix, the way a console that
// forwards to alarmd mounts it under /alarmd, and runs the installer against it.
type installerHost struct {
	t      *testing.T
	server *httptest.Server
	base   string
}

func newInstallerHost(t *testing.T, release *Release, tamper func(http.ResponseWriter, *http.Request) bool) *installerHost {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("the installer targets Linux and macOS")
	}
	for _, tool := range []string{"sh", "curl", "tar"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	mux := http.NewServeMux()
	release.Register(mux)
	prefixed := http.StripPrefix("/admin/alarmd", mux)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if tamper != nil && tamper(response, request) {
			return
		}
		prefixed.ServeHTTP(response, request)
	}))
	t.Cleanup(server.Close)
	return &installerHost{t: t, server: server, base: server.URL + "/admin/alarmd"}
}

func (h *installerHost) install(dir string, args ...string) (string, error) {
	h.t.Helper()
	command := exec.Command("sh", append([]string{"-s", "--", "--server", h.base + "/", "--install-dir", dir}, args...)...)
	command.Stdin = strings.NewReader(string(installer))
	command.Env = append(os.Environ(), "PATH="+os.Getenv("PATH"))
	output, err := command.CombinedOutput()
	return string(output), err
}

func installedVersion(t *testing.T, dir string) string {
	t.Helper()
	output, err := exec.Command(filepath.Join(dir, Executable), "--version").Output()
	if err != nil {
		t.Fatalf("the installed alarmd-cli does not run: %v", err)
	}
	return string(output)
}

func TestTheInstallerInstallsTheServersVersionUnderAPrefix(t *testing.T) {
	host := newInstallerHost(t, LoadFS(os.DirFS(packed(t, testVersion)), testVersion), nil)
	dir := filepath.Join(t.TempDir(), "bin")
	output, err := host.install(dir)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, output)
	}
	if !strings.Contains(installedVersion(t, dir), `"version":"`+testVersion+`"`) {
		t.Fatalf("installed %s", installedVersion(t, dir))
	}
	if !strings.Contains(output, "alarmd-cli "+testVersion+" installed at "+filepath.Join(dir, Executable)) || !strings.Contains(output, "is not on PATH") {
		t.Fatalf("output:\n%s", output)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("the install directory holds %d entries, want the executable alone", len(entries))
	}

	// The deployment is the authority: a server on an older version gets
	// its own version installed over the newer one, and the change is said.
	older := newInstallerHost(t, LoadFS(os.DirFS(packed(t, "0.4.4700")), "0.4.4700"), nil)
	output, err = older.install(dir)
	if err != nil {
		t.Fatalf("install older: %v\n%s", err, output)
	}
	if !strings.Contains(output, "alarmd-cli "+testVersion+" -> 0.4.4700 installed") || !strings.Contains(installedVersion(t, dir), `"version":"0.4.4700"`) {
		t.Fatalf("output:\n%s", output)
	}
}

func TestTheInstallerLeavesTheOldBinaryOnAnyFailure(t *testing.T) {
	root := packed(t, testVersion)
	release := LoadFS(os.DirFS(root), testVersion)
	dir := t.TempDir()
	if output, err := newInstallerHost(t, release, nil).install(dir); err != nil {
		t.Fatalf("install: %v\n%s", err, output)
	}
	before := installedVersion(t, dir)
	mislabelled := releaseRoot(t)
	if err := Pack(mislabelled, testVersion, fakeBuild("0.4.1")); err != nil {
		t.Fatal(err)
	}

	failures := map[string]struct {
		host *installerHost
		want string
	}{
		"a corrupted archive": {newInstallerHost(t, release, func(response http.ResponseWriter, request *http.Request) bool {
			if !strings.HasSuffix(request.URL.Path, ".tar.gz") {
				return false
			}
			_, _ = response.Write([]byte("not the archive"))
			return true
		}), "SHA-256 of"},
		"a checksum file for another archive": {newInstallerHost(t, release, func(response http.ResponseWriter, request *http.Request) bool {
			if !strings.HasSuffix(request.URL.Path, ".sha256") {
				return false
			}
			_, _ = response.Write([]byte(ChecksumLine(strings.Repeat("0", 64), "other.tar.gz")))
			return true
		}), "the checksum file names other.tar.gz"},
		"a version the deployment does not carry": {newInstallerHost(t, release, func(response http.ResponseWriter, request *http.Request) bool {
			if !strings.HasSuffix(request.URL.Path, "/api/cli/version") {
				return false
			}
			_, _ = response.Write([]byte("0.4.4802\n"))
			return true
		}), "answered HTTP 404"},
		"an executable reporting another version": {newInstallerHost(t, LoadFS(os.DirFS(mislabelled), testVersion), nil), "not version " + testVersion},
		"a deployment without a release":          {newInstallerHost(t, LoadFS(os.DirFS(t.TempDir()), testVersion), nil), CodeUnavailable},
		"an unreachable server": {func() *installerHost {
			h := newInstallerHost(t, release, nil)
			h.server.Close()
			return h
		}(), "cannot reach"},
	}
	for name, failure := range failures {
		t.Run(name, func(t *testing.T) {
			output, err := failure.host.install(dir)
			if err == nil || !strings.Contains(output, failure.want) {
				t.Fatalf("install: %v, want a failure naming %q:\n%s", err, failure.want, output)
			}
			if after := installedVersion(t, dir); after != before {
				t.Fatalf("the installed binary changed to %s", after)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 1 {
				t.Fatalf("the failure left %d entries in the install directory", len(entries))
			}
		})
	}
}

// The temporary directory may be mounted noexec; the install directory
// cannot be, so the new binary is checked from there. The executable here
// runs only from the install directory, as on such a machine.
func TestTheInstallerChecksTheBinaryWhereItWillLive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bin")
	root := releaseRoot(t)
	err := Pack(root, testVersion, func(_ Target, dst string) error {
		script := "#!/bin/sh\ncase \"$0\" in \"" + dir + "\"/*) ;; *) exit 126 ;; esac\necho '{\"version\":\"" + testVersion + "\"}'\n"
		return os.WriteFile(dst, []byte(script), 0o755)
	})
	if err != nil {
		t.Fatal(err)
	}
	host := newInstallerHost(t, LoadFS(os.DirFS(root), testVersion), nil)
	if output, err := host.install(dir); err != nil {
		t.Fatalf("install: %v\n%s", err, output)
	}
	if !strings.Contains(installedVersion(t, dir), testVersion) {
		t.Fatal("the installed binary does not report the release")
	}
}

func TestTheInstallerRefusesAServerWithCredentials(t *testing.T) {
	for _, server := range []string{"ftp://example.test", "https://user:secret@example.test", "https://example.test/?a=b", "https://example.test/#x", ""} {
		command := exec.Command("sh", "-s", "--", "--server", server, "--install-dir", t.TempDir())
		command.Stdin = strings.NewReader(string(installer))
		if output, err := command.CombinedOutput(); err == nil {
			t.Errorf("--server %q accepted:\n%s", server, output)
		}
	}
}
