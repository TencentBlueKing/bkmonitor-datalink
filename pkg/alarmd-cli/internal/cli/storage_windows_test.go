package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func assertPrivatePath(t *testing.T, path string, directory bool) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.Equals(user.User.Sid) {
		t.Fatalf("unexpected owner: %v", err)
	}
	flags := ""
	if directory {
		flags = "OICI"
	}
	want := "D:P(A;" + flags + ";FA;;;" + user.User.Sid.String() + ")(A;" + flags + ";FA;;;SY)"
	expected, err := windows.SecurityDescriptorFromString(want)
	if err != nil {
		t.Fatal(err)
	}
	// Canonical SDDL also normalizes well-known user SIDs.
	actual := sd.String()
	// NTFS may add AUTO_INHERITED when renaming within the same directory;
	// it does not remove protection or add inherited ACEs.
	if i := strings.Index(actual, "D:"); i < 0 || strings.Replace(actual[i:], "D:PAI", "D:P", 1) != expected.String() {
		t.Fatalf("unsafe DACL for %s: %s", path, actual)
	}
}

func TestWindowsReplacementFailurePreservesOldFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	if err := atomicWrite(path, []byte("old")); err != nil {
		t.Fatal(err)
	}
	p, _ := windows.UTF16PtrFromString(path)
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	if err := atomicWrite(path, []byte("new")); err == nil {
		t.Fatal("replacement succeeded without delete sharing")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "old" {
		t.Fatalf("old file lost: %q, %v", data, err)
	}
	temps, _ := filepath.Glob(filepath.Join(dir, ".alarmd-*"))
	if len(temps) != 0 {
		t.Fatal("temporary files leaked")
	}
}

func TestWindowsRejectsRedirectedAndLinkedStorage(t *testing.T) {
	if err := validateStoragePath(`\\server\share\config`); err == nil {
		t.Fatal("network storage accepted")
	}
	dir := t.TempDir()
	original := filepath.Join(dir, "original")
	if err := os.WriteFile(original, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linked")
	if err := os.Link(original, link); err != nil {
		t.Fatal(err)
	}
	if err := regularFile(link); err == nil {
		t.Fatal("hard-linked credentials accepted")
	}
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(dir, "junction")
	if out, err := exec.Command("cmd.exe", "/c", "mklink", "/J", junction, target).CombinedOutput(); err != nil {
		t.Fatalf("create junction: %v %s", err, out)
	}
	if err := secureDir(filepath.Join(junction, "child")); err == nil {
		t.Fatal("redirected configuration directory accepted")
	}
	if _, err := os.Stat(filepath.Join(target, "child")); !os.IsNotExist(err) {
		t.Fatal("rejected storage created a child through junction")
	}
}
