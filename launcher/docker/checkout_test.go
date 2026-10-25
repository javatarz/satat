package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/javatarz/satat/launcher"
)

type entry struct {
	name, body, link string
	typ              byte
}

func tarOf(t *testing.T, entries ...entry) io.Reader {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: 0o644, Linkname: e.link, Uid: 0, Gid: 0}
		switch e.typ {
		case 0, tar.TypeReg:
			h.Typeflag, h.Size = tar.TypeReg, int64(len(e.body))
		case tar.TypeDir:
			h.Mode = 0o755
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			tw.Write([]byte(e.body))
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

// aCheckout is a stand-in for the worker's clone: a repo with a .git directory that holds no
// credential, a file and a symlink.
func aCheckout(t *testing.T) io.Reader {
	return tarOf(t,
		entry{name: ".git/", typ: tar.TypeDir},
		entry{name: ".git/HEAD", body: "ref: refs/heads/main\n"},
		entry{name: ".git/config", body: "[core]\n\tbare = false\n[remote \"origin\"]\n\turl = https://github.com/a/b.git\n"},
		entry{name: "README.md", body: "hello\n"},
		entry{name: "link", typ: tar.TypeSymlink, link: "README.md"},
	)
}

func rewritten(t *testing.T, src io.Reader) (map[string]*tar.Header, error) {
	t.Helper()
	var out bytes.Buffer
	if err := rewriteCheckout(&out, src, 10001, 10001); err != nil {
		return nil, err
	}
	got := map[string]*tar.Header{}
	tr := tar.NewReader(&out)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return got, nil
		}
		if err != nil {
			t.Fatal(err)
		}
		got[h.Name] = h
	}
}

func TestCheckoutIsRootedAndOwnedByTheStepUser(t *testing.T) {
	got, err := rewritten(t, aCheckout(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"repo/", "repo/.git/", "repo/.git/HEAD", "repo/README.md", "repo/link"} {
		h := got[n]
		if h == nil {
			t.Errorf("missing %s", n)
			continue
		}
		if h.Uid != 10001 || h.Gid != 10001 {
			t.Errorf("%s is owned by %d:%d", n, h.Uid, h.Gid)
		}
	}
	if got["repo/link"].Linkname != "README.md" {
		t.Error("symlink not kept")
	}
}

func TestCheckoutRefusesWhatCouldEscapeOrIsNotAFile(t *testing.T) {
	for name, tc := range map[string]struct {
		entries []entry
		want    string
	}{
		"parent path":    {[]entry{{name: "../etc/passwd", body: "x"}}, "leaves the archive"},
		"nested parent":  {[]entry{{name: "a/../../x", body: "x"}}, "leaves the archive"},
		"absolute path":  {[]entry{{name: "/etc/passwd", body: "x"}}, "leaves the archive"},
		"through a link": {[]entry{{name: "d", typ: tar.TypeSymlink, link: "/etc/satat/out"}, {name: "d/x", body: "x"}}, "under the symlink"},
		"hard link":      {[]entry{{name: "h", typ: tar.TypeLink, link: "x"}}, "only directories"},
		"device":         {[]entry{{name: "n", typ: tar.TypeChar}}, "only directories"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := rewritten(t, tarOf(t, tc.entries...)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// The step sees the checkout at /etc/satat/repo, owned by its user, and can write to it.
func TestStepGetsTheCheckoutBeforeItStarts(t *testing.T) {
	api := dockerAPI(t)
	lane := shLane("checkout", `cat /etc/satat/repo/README.md; cat /etc/satat/repo/link; stat -c '%u:%g' /etc/satat/repo/README.md; echo more > /etc/satat/repo/new && echo writable`)
	l := newLauncher(t, api, []launcher.Lane{lane})
	res, err := l.Launch(context.Background(), launcher.Step{Run: "co", Name: "c", Lane: "checkout", Checkout: aCheckout(t)})
	if err != nil {
		t.Fatal(err)
	}
	if want := "hello\nhello\n10001:10001\nwritable\n"; res.ExitCode != 0 || string(res.Stdout) != want {
		t.Errorf("exit %d, stdout %q (stderr %q), want %q", res.ExitCode, res.Stdout, res.Stderr, want)
	}
}

func TestABadCheckoutFailsTheLaunch(t *testing.T) {
	api := dockerAPI(t)
	l := newLauncher(t, api, []launcher.Lane{shLane("badco", "echo ran")})
	res, err := l.Launch(context.Background(), launcher.Step{Run: "bad", Name: "c", Lane: "badco", Checkout: tarOf(t, entry{name: "../x", body: "x"})})
	if err == nil {
		t.Fatal("launched with a checkout that leaves its root")
	}
	if strings.Contains(string(res.Stdout), "ran") {
		t.Error("the step ran")
	}
}
