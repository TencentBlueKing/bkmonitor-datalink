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
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testVersion = "0.4.4801"

// fakeBuild writes, for each target, an executable that reports version the
// way alarmd-cli --version does.
func fakeBuild(version string) Build {
	return func(target Target, dst string) error {
		script := "#!/bin/sh\necho '{\"channel_version\":\"alarmd-ob/v1\",\"name\":\"alarmd-cli\",\"version\":\"" + version + "\"}'\n# " + target.Name + "\n"
		return os.WriteFile(dst, []byte(script), 0o755)
	}
}

// releaseRoot is an empty release root, as the committed one is.
func releaseRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, Placeholder), []byte("placeholder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func packed(t *testing.T, version string) string {
	t.Helper()
	root := releaseRoot(t)
	if err := Pack(root, version, fakeBuild(version)); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	return root
}

func readManifest(t *testing.T, root string) Manifest {
	t.Helper()
	document, err := os.ReadFile(filepath.Join(root, ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(document, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestAPackedReleaseLoadsVerifiesAndListsEveryTarget(t *testing.T) {
	root := packed(t, testVersion)
	release := LoadFS(os.DirFS(root), testVersion)
	if err := release.Problem(); err != nil {
		t.Fatalf("Problem: %v", err)
	}
	if err := release.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got, want := strings.Join(release.TargetNames(), ","), "darwin-arm64,darwin-x64,linux-arm64,linux-x64"; got != want {
		t.Fatalf("targets %s, want %s", got, want)
	}
	for _, target := range Targets {
		artifact := readManifest(t, root).Artifacts[target.Name]
		if artifact.File != "alarmd-cli-"+testVersion+"-"+target.Name+".tar.gz" {
			t.Errorf("%s is named %s", target.Name, artifact.File)
		}
	}
	if _, err := os.Stat(filepath.Join(root, Placeholder)); err != nil {
		t.Fatalf("the placeholder did not survive Pack: %v", err)
	}
}

// One version packed twice -- as the amd64 and arm64 image builds each do --
// gives the same bytes, so every replica answers the same digest.
func TestAnArchiveEntryCarriesNothingOfTheBuildMachine(t *testing.T) {
	archive, err := archiveOf([]byte("binary"))
	if err != nil {
		t.Fatal(err)
	}
	decompressor, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	header, err := tar.NewReader(decompressor).Next()
	if err != nil {
		t.Fatal(err)
	}
	if !header.ModTime.Equal(archiveTime) || header.Uid != 0 || header.Gid != 0 || header.Uname != "" || header.Gname != "" || header.Mode != 0o755 {
		t.Fatalf("the entry carries %v uid %d gid %d %q %q mode %o", header.ModTime, header.Uid, header.Gid, header.Uname, header.Gname, header.Mode)
	}
	if !decompressor.Header.ModTime.IsZero() || decompressor.Header.Name != "" {
		t.Fatalf("the gzip header carries %v %q", decompressor.Header.ModTime, decompressor.Header.Name)
	}
}

func TestPackIsAFunctionOfTheExecutablesAlone(t *testing.T) {
	first, second := readManifest(t, packed(t, testVersion)), readManifest(t, packed(t, testVersion))
	for name, artifact := range first.Artifacts {
		if second.Artifacts[name] != artifact {
			t.Errorf("%s packed twice differs: %+v and %+v", name, artifact, second.Artifacts[name])
		}
	}
}

func TestPackClearsAnEarlierReleaseAndRefusesANonReleaseVersion(t *testing.T) {
	root := packed(t, "0.4.1")
	if err := Pack(root, testVersion, fakeBuild(testVersion)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "0.4.1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the earlier release is still under the root: %v", err)
	}
	for _, version := range []string{"dev", "0.4.x", "v0.4.1", "0.4.01", "0.4.1+build"} {
		if err := Pack(releaseRoot(t), version, fakeBuild(version)); err == nil {
			t.Errorf("Pack accepted version %q", version)
		}
	}
	if err := Pack(releaseRoot(t), "0.2.0-ci.123", fakeBuild("0.2.0-ci.123")); err != nil {
		t.Errorf("Pack refused a pre-release version: %v", err)
	}
	// A root without the placeholder is refused, and left as it was.
	stranger := t.TempDir()
	_ = os.WriteFile(filepath.Join(stranger, "go.mod"), []byte("module example.test\n"), 0o644)
	if err := Pack(stranger, testVersion, fakeBuild(testVersion)); err == nil {
		t.Error("Pack accepted a root without the placeholder")
	}
	if _, err := os.Stat(filepath.Join(stranger, "go.mod")); err != nil {
		t.Errorf("Pack removed a file from a root it refused: %v", err)
	}
}

func TestLoadTellsNoReleaseFromABrokenOne(t *testing.T) {
	if err := LoadFS(os.DirFS(t.TempDir()), testVersion).Problem(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("an empty root: %v, want unavailable", err)
	}
	if err := (*Release)(nil).Problem(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a nil release: %v, want unavailable", err)
	}
	// The placeholder alone is what go build and go test embed. The
	// package's own embed is not read here: a local make build leaves a
	// release in it, and the test must not depend on that.
	if err := LoadFS(os.DirFS(releaseRoot(t)), testVersion).Problem(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("the placeholder alone: %v, want unavailable", err)
	}
	// Whatever was embedded, a binary stamped dev has no usable release.
	if err := Load("dev").Problem(); err == nil {
		t.Fatal("Load(dev) found a usable release")
	}

	broken := map[string]func(t *testing.T, root string){
		"another version": func(t *testing.T, root string) {},
		"an archive cut short": func(t *testing.T, root string) {
			file := filepath.Join(root, testVersion, ArtifactName(testVersion, "linux-x64"))
			content, _ := os.ReadFile(file)
			_ = os.WriteFile(file, content[:len(content)-1], 0o644)
		},
		"a checksum file that disagrees": func(t *testing.T, root string) {
			_ = os.WriteFile(filepath.Join(root, testVersion, ArtifactName(testVersion, "linux-x64")+".sha256"), []byte(ChecksumLine(strings.Repeat("0", 64), ArtifactName(testVersion, "linux-x64"))), 0o644)
		},
		// Named, sized and summed like a real entry, so only the target list
		// can refuse it.
		"an unknown target": func(t *testing.T, root string) {
			from, to := ArtifactName(testVersion, "linux-x64"), ArtifactName(testVersion, "windows-x64")
			content, _ := os.ReadFile(filepath.Join(root, testVersion, from))
			_ = os.WriteFile(filepath.Join(root, testVersion, to), content, 0o644)
			rewrite(t, root, func(m *Manifest) {
				a := m.Artifacts["linux-x64"]
				a.File = to
				m.Artifacts["windows-x64"] = a
			})
			_ = os.WriteFile(filepath.Join(root, testVersion, to+".sha256"), []byte(ChecksumLine(readManifest(t, root).Artifacts["windows-x64"].SHA256, to)), 0o644)
		},
		// Another target's archive, whole and consistent, filed under this
		// target: only the name rule can tell.
		"another target's archive": func(t *testing.T, root string) {
			rewrite(t, root, func(m *Manifest) { m.Artifacts["linux-x64"] = m.Artifacts["darwin-x64"] })
		},
		"a misnamed file": func(t *testing.T, root string) {
			rewrite(t, root, func(m *Manifest) {
				a := m.Artifacts["linux-x64"]
				a.File = "alarmd-cli.tar.gz"
				m.Artifacts["linux-x64"] = a
			})
		},
		"another schema": func(t *testing.T, root string) { rewrite(t, root, func(m *Manifest) { m.SchemaVersion = 2 }) },
		"no artifact":    func(t *testing.T, root string) { rewrite(t, root, func(m *Manifest) { m.Artifacts = nil }) },
		"a missing archive": func(t *testing.T, root string) {
			_ = os.Remove(filepath.Join(root, testVersion, ArtifactName(testVersion, "darwin-x64")))
		},
	}
	for name, breakIt := range broken {
		t.Run(name, func(t *testing.T) {
			root := packed(t, testVersion)
			breakIt(t, root)
			version := testVersion
			if name == "another version" {
				version = "0.4.4802"
			}
			if err := LoadFS(os.DirFS(root), version).Problem(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Problem: %v, want invalid", err)
			}
		})
	}
}

func rewrite(t *testing.T, root string, change func(*Manifest)) {
	t.Helper()
	manifest := readManifest(t, root)
	change(&manifest)
	document, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(root, ManifestFile), document, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Load checks sizes; Verify, the build's check, recomputes the digests and
// catches what a size cannot.
func TestVerifyCatchesWhatLoadDoesNotHash(t *testing.T) {
	root := packed(t, testVersion)
	file := filepath.Join(root, testVersion, ArtifactName(testVersion, "linux-arm64"))
	content, _ := os.ReadFile(file)
	// The gzip header's OS byte: the archive still unpacks to the same
	// executable at the same size, so only the archive digest differs.
	content[9] ^= 0xff
	_ = os.WriteFile(file, content, 0o644)
	release := LoadFS(os.DirFS(root), testVersion)
	if err := release.Problem(); err != nil {
		t.Fatalf("Problem: %v", err)
	}
	if err := release.Verify(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Verify: %v, want invalid", err)
	}

	root = packed(t, testVersion)
	rewrite(t, root, func(m *Manifest) {
		a := m.Artifacts["darwin-arm64"]
		a.ExecutableSHA256 = strings.Repeat("1", 64)
		m.Artifacts["darwin-arm64"] = a
	})
	if err := LoadFS(os.DirFS(root), testVersion).Verify(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Verify with a wrong executable digest: %v, want invalid", err)
	}
}

// A loaded release keeps the manifest and the checksum lines, never the
// archives: every alarmd process loads it, and few ever serve a download.
func TestALoadedReleaseDoesNotHoldTheArchives(t *testing.T) {
	root := releaseRoot(t)
	executable := make([]byte, 4<<20)
	if _, err := rand.Read(executable); err != nil {
		t.Fatal(err)
	}
	err := Pack(root, testVersion, func(_ Target, dst string) error { return os.WriteFile(dst, executable, 0o755) })
	if err != nil {
		t.Fatal(err)
	}
	executable = nil
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	release := LoadFS(os.DirFS(root), testVersion)
	runtime.GC()
	runtime.ReadMemStats(&after)
	if err := release.Problem(); err != nil {
		t.Fatal(err)
	}
	if grown := int64(after.HeapAlloc) - int64(before.HeapAlloc); grown > 1<<20 {
		t.Fatalf("loading a %d MB release grew the live heap by %d bytes", 4*len(Targets), grown)
	}
	runtime.KeepAlive(release)
}

func TestAnArchiveCarriesTheExecutableAlone(t *testing.T) {
	archive, err := archiveOf([]byte("binary"))
	if err != nil {
		t.Fatal(err)
	}
	if executable, err := ReadExecutable(archive); err != nil || string(executable) != "binary" {
		t.Fatalf("ReadExecutable: %q, %v", executable, err)
	}
	if _, err := ReadExecutable(append(bytes.Clone(archive[:10]), 0)); err == nil {
		t.Fatal("ReadExecutable accepted a broken archive")
	}
}

func serve(t *testing.T, release *Release, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	release.Register(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(method, path, nil))
	return response
}

func errorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Status string `json:"status"`
		Error  struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Status != "error" {
		t.Fatalf("not an alarmd error body: %q (%v)", response.Body.String(), err)
	}
	return body.Error.Code
}

func TestTheRoutesServeTheReleaseAndNothingElse(t *testing.T) {
	root := packed(t, testVersion)
	release := LoadFS(os.DirFS(root), testVersion)
	archive := ArtifactName(testVersion, "linux-x64")

	response := serve(t, release, http.MethodGet, VersionPath)
	if response.Code != http.StatusOK || response.Body.String() != testVersion+"\n" {
		t.Fatalf("version: %d %q", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers: %v", response.Header())
	}
	document, _ := os.ReadFile(filepath.Join(root, ManifestFile))
	if response := serve(t, release, http.MethodGet, ManifestPath); response.Code != http.StatusOK || response.Body.String() != string(document) || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("manifest: %d %q", response.Code, response.Header().Get("Content-Type"))
	}
	content, _ := os.ReadFile(filepath.Join(root, testVersion, archive))
	if response := serve(t, release, http.MethodGet, ReleasesPath+testVersion+"/"+archive); response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), content) {
		t.Fatalf("archive: %d, %d bytes", response.Code, response.Body.Len())
	}
	if response := serve(t, release, http.MethodGet, ReleasesPath+testVersion+"/"+archive+".sha256"); response.Code != http.StatusOK || response.Body.String() != ChecksumLine(digest(content), archive) {
		t.Fatalf("checksum: %d %q", response.Code, response.Body.String())
	}
	if response := serve(t, release, http.MethodGet, InstallPath); response.Code != http.StatusOK || !strings.HasPrefix(response.Body.String(), "#!/bin/sh\n") {
		t.Fatalf("installer: %d", response.Code)
	}
	if response := serve(t, release, http.MethodHead, VersionPath); response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Fatalf("HEAD: %d, %d bytes", response.Code, response.Body.Len())
	}

	for _, path := range []string{
		ReleasesPath + "0.4.4800/" + ArtifactName("0.4.4800", "linux-x64"),
		ReleasesPath + testVersion + "/" + ManifestFile,
		ReleasesPath + testVersion + "/../" + ManifestFile,
		ReleasesPath + testVersion,
		ReleasesPath + testVersion + "/",
		ReleasesPath,
	} {
		response := serve(t, release, http.MethodGet, path)
		if response.Code == http.StatusOK {
			t.Errorf("%s answered 200", path)
		} else if response.Code == http.StatusNotFound && errorCode(t, response) != CodeNotFound {
			t.Errorf("%s answered 404 without %s", path, CodeNotFound)
		}
	}
	if response := serve(t, release, http.MethodPost, VersionPath); response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST: %d", response.Code)
	}
}

func TestEveryRouteSaysWhyThereIsNoRelease(t *testing.T) {
	root := packed(t, testVersion)
	cases := map[string]struct {
		release *Release
		code    string
	}{
		"nil":         {nil, CodeUnavailable},
		"unavailable": {LoadFS(os.DirFS(t.TempDir()), testVersion), CodeUnavailable},
		"invalid":     {LoadFS(os.DirFS(root), "0.4.4802"), CodeInvalid},
	}
	for name, c := range cases {
		for _, path := range []string{VersionPath, ManifestPath, InstallPath, ReleasesPath + testVersion + "/" + ArtifactName(testVersion, "linux-x64")} {
			response := serve(t, c.release, http.MethodGet, path)
			if response.Code != http.StatusServiceUnavailable || errorCode(t, response) != c.code {
				t.Errorf("%s %s: %d %s", name, path, response.Code, response.Body.String())
			}
		}
	}
}

// The installer is served as written, whatever the request: it carries no
// address, so one script serves every deployment and every prefix.
func TestTheInstallerCarriesNoAddress(t *testing.T) {
	release := LoadFS(os.DirFS(packed(t, testVersion)), testVersion)
	mux := http.NewServeMux()
	release.Register(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	response, err := http.Get(server.URL + InstallPath)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if !bytes.Equal(body, installer) || bytes.Contains(body, []byte(server.URL)) {
		t.Fatal("the installer served is not the script as written")
	}
}
