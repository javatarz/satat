package deploy

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureKeyCreatesOnceAndKeepsMode0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	calls := 0
	gen := func() (string, error) { calls++; return "first", nil }

	created, err := EnsureKey(path, "POSTGRES_PASSWORD", gen)
	if err != nil || !created {
		t.Fatalf("first call: created=%v err=%v", created, err)
	}
	created, err = EnsureKey(path, "POSTGRES_PASSWORD", func() (string, error) { return "second", nil })
	if err != nil || created {
		t.Fatalf("second call: created=%v err=%v", created, err)
	}
	if calls != 1 {
		t.Errorf("gen called %d times", calls)
	}
	v, ok, err := ReadKey(path, "POSTGRES_PASSWORD")
	if err != nil || !ok || v != "first" {
		t.Errorf("ReadKey = %q %v %v", v, ok, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
}

func TestEnsureKeyKeepsOtherLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	orig := "# comment\n\nHATCHET_CLIENT_TOKEN=abc=\nOTHER=x y\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	created, err := EnsureKey(path, "POSTGRES_PASSWORD", func() (string, error) { return "pw", nil })
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	got, _ := os.ReadFile(path)
	if want := orig + "POSTGRES_PASSWORD=pw\n"; string(got) != want {
		t.Errorf("got %q want %q", got, want)
	}
	if v, ok, _ := ReadKey(path, "HATCHET_CLIENT_TOKEN"); !ok || v != "abc=" {
		t.Errorf("ReadKey token = %q %v", v, ok)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
}

func TestEnsureKeyAddsNewlineWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("A=1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureKey(path, "B", func() (string, error) { return "2", nil }); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "A=1\nB=2\n" {
		t.Errorf("got %q", got)
	}
}

func TestReadKeyMissingFileOrKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if _, ok, err := ReadKey(path, "X"); ok || err != nil {
		t.Errorf("missing file: ok=%v err=%v", ok, err)
	}
	_ = os.WriteFile(path, []byte("# X=1\nY=2\n"), 0o600)
	if _, ok, err := ReadKey(path, "X"); ok || err != nil {
		t.Errorf("commented key: ok=%v err=%v", ok, err)
	}
}

func TestRandomPassword(t *testing.T) {
	a, err := RandomPassword()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := RandomPassword()
	raw, err := base64.RawURLEncoding.DecodeString(a)
	if err != nil || len(raw) != 32 {
		t.Errorf("not 32 bytes of raw base64url: %q (%v)", a, err)
	}
	if a == b {
		t.Error("two passwords equal")
	}
}

func TestRemoveKeyDropsOnlyThatKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	orig := "# comment\n\nPOSTGRES_PASSWORD=pw\nHATCHET_CLIENT_TOKEN=eyJold\nOTHER=x y"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	removed, err := RemoveKey(path, "HATCHET_CLIENT_TOKEN")
	if err != nil || !removed {
		t.Fatalf("removed=%v err=%v", removed, err)
	}
	got, _ := os.ReadFile(path)
	if want := "# comment\n\nPOSTGRES_PASSWORD=pw\nOTHER=x y"; string(got) != want {
		t.Errorf("got %q want %q", got, want)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
}

func TestRemoveKeyMissingFileOrKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if removed, err := RemoveKey(path, "X"); removed || err != nil {
		t.Errorf("missing file: removed=%v err=%v", removed, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("missing file was created: %v", err)
	}
	const orig = "# X=1\nY=2\n"
	_ = os.WriteFile(path, []byte(orig), 0o600)
	if removed, err := RemoveKey(path, "X"); removed || err != nil {
		t.Errorf("absent key: removed=%v err=%v", removed, err)
	}
	if got, _ := os.ReadFile(path); string(got) != orig {
		t.Errorf("file changed: %q", got)
	}
}
