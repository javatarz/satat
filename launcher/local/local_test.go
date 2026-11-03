package local

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestStepEnvDropsWorkerCredentialsAndSession(t *testing.T) {
	got := stepEnv([]string{
		"PATH=/bin", "HOME=/h",
		"SATAT_GITHUB_TOKEN=r", "SATAT_PUSH_TOKEN=w", "HATCHET_CLIENT_TOKEN=h", "GH_TOKEN=g", "GITHUB_TOKEN=g",
		"CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=s",
		"CLAUDE_CODE_OAUTH_TOKEN=o", "SATAT_GIT_NAME=n", "SATAT_MISE_DEFAULTS=/d",
	})
	want := []string{"PATH=/bin", "HOME=/h", "CLAUDE_CODE_OAUTH_TOKEN=o", "SATAT_GIT_NAME=n", "SATAT_MISE_DEFAULTS=/d"}
	if !slices.Equal(got, want) {
		t.Errorf("stepEnv = %v, want %v", got, want)
	}
}

func tarOf(t *testing.T, entries ...*tar.Header) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, h := range entries {
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(h.Name))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(h.Name)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestExtractWritesFilesDirsAndLinks(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "repo")
	err := extract(tarOf(t,
		&tar.Header{Typeflag: tar.TypeDir, Name: "a/", Mode: 0o755},
		&tar.Header{Typeflag: tar.TypeReg, Name: "a/f.txt", Mode: 0o644},
		&tar.Header{Typeflag: tar.TypeSymlink, Name: "l", Linkname: "a/f.txt"},
	), dst)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "l")); err != nil || string(b) != "a/f.txt" {
		t.Errorf("read through link = %q, %v", b, err)
	}
}

func TestExtractRefusesEscapes(t *testing.T) {
	for name, entries := range map[string][]*tar.Header{
		"parent":        {{Typeflag: tar.TypeReg, Name: "../x"}},
		"absolute":      {{Typeflag: tar.TypeReg, Name: "/x"}},
		"under symlink": {{Typeflag: tar.TypeSymlink, Name: "l", Linkname: "/"}, {Typeflag: tar.TypeReg, Name: "l/x"}},
		"device":        {{Typeflag: tar.TypeChar, Name: "d"}},
	} {
		if err := extract(tarOf(t, entries...), filepath.Join(t.TempDir(), "repo")); err == nil {
			t.Errorf("%s: extract accepted it", name)
		}
	}
}

func TestSetupResolvesDir(t *testing.T) {
	if _, err := os.Stat("/usr/bin/git"); err != nil {
		t.Skip("no git")
	}
	t.Setenv("PATH", fakeBins(t, "claude", "mise", "git")+string(os.PathListSeparator)+os.Getenv("PATH"))
	l := New(nil, t.TempDir()+"/steps/", nil)
	if err := l.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(l.Dir, "/") || !filepath.IsAbs(l.Dir) {
		t.Errorf("Dir = %q, want a clean absolute path for MISE_CEILING_PATHS", l.Dir)
	}
	if real, _ := filepath.EvalSymlinks(l.Dir); real != l.Dir {
		t.Errorf("Dir = %q, want the real path %q", l.Dir, real)
	}
}

func fakeBins(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
