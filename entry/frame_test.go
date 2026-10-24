package entry

import (
	"archive/tar"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func frame(t *testing.T, r Result) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := Write(&b, r); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestFrameRoundTrip(t *testing.T) {
	want := Result{
		ExitCode:       3,
		OOMKilled:      true,
		TimedOut:       true,
		Stdout:         []byte("line one\n" + Begin + "\n\x00\xff binary\n" + End + "\n"),
		Stderr:         []byte("err\n"),
		LogsTruncated:  true,
		Output:         []byte("tar"),
		FilesTruncated: true,
	}
	log := append([]byte("noise before\n"), frame(t, want)...)
	got, ok, err := Parse(log)
	if err != nil || !ok {
		t.Fatalf("Parse: ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestFrameIsOneJSONLine(t *testing.T) {
	b := frame(t, Result{ExitCode: 1, Stdout: []byte("a\nb\n")})
	lines := bytes.Split(bytes.TrimSuffix(b, []byte("\n")), []byte("\n"))
	if len(lines) != 3 || string(lines[0]) != Begin || string(lines[2]) != End {
		t.Fatalf("frame is not three lines: %q", b)
	}
	if !bytes.HasPrefix(lines[1], []byte(`{"exit_code":1,`)) {
		t.Fatalf("frame body is not the raw JSON result: %q", lines[1])
	}
}

// A result at every cap must fit well inside kubelet's 10 MiB log rotation size.
func TestMaxFrameFitsTheLog(t *testing.T) {
	b := frame(t, Result{
		ExitCode: 1,
		Stdout:   bytes.Repeat([]byte{0xff}, MaxStream),
		Stderr:   bytes.Repeat([]byte{0xff}, MaxStream),
		Output:   bytes.Repeat([]byte{0xff}, MaxOutput),
	})
	if len(b) >= 9<<20 {
		t.Fatalf("a max-cap frame is %d bytes, want under 9 MiB", len(b))
	}
}

func TestParseRejectsCorruptFrame(t *testing.T) {
	log := []byte(Begin + "\n{not json\n" + End + "\n")
	if _, ok, err := Parse(log); ok || err == nil {
		t.Fatalf("ok=%v err=%v, want a decode error", ok, err)
	}
}

func TestParseTakesLastFrame(t *testing.T) {
	log := append(frame(t, Result{ExitCode: 1}), frame(t, Result{ExitCode: 2})...)
	got, ok, err := Parse(log)
	if err != nil || !ok || got.ExitCode != 2 {
		t.Fatalf("got %+v ok=%v err=%v, want exit 2", got, ok, err)
	}
}

func TestParseIgnoresIncompleteFrame(t *testing.T) {
	complete := frame(t, Result{ExitCode: 7})
	partial := frame(t, Result{ExitCode: 9})
	partial = partial[:bytes.Index(partial, []byte(End))]

	got, ok, err := Parse(append(append([]byte{}, complete...), partial...))
	if err != nil || !ok || got.ExitCode != 7 {
		t.Fatalf("got %+v ok=%v err=%v, want exit 7", got, ok, err)
	}
	if _, ok, err := Parse(partial); ok || err != nil {
		t.Fatalf("only incomplete: ok=%v err=%v, want false, nil", ok, err)
	}
	if _, ok, err := Parse(nil); ok || err != nil {
		t.Fatalf("empty: ok=%v err=%v, want false, nil", ok, err)
	}
}

func TestPackUnpack(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.txt"), []byte("a"))
	write(t, filepath.Join(dir, "sub", "b.txt"), []byte("b"))
	if err := os.Symlink(filepath.Join(dir, "a.txt"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	tb, truncated, err := PackDir(dir)
	if err != nil || truncated {
		t.Fatalf("PackDir: truncated=%v err=%v", truncated, err)
	}
	files, err := UnpackFiles(tb)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]byte{"a.txt": []byte("a"), "sub/b.txt": []byte("b")}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("got %q, want %q", files, want)
	}
}

func TestPackCaps(t *testing.T) {
	dir := t.TempDir()
	for i := range MaxFiles + 1 {
		write(t, filepath.Join(dir, fmt.Sprintf("f%02d", i)), []byte("x"))
	}
	tb, truncated, err := PackDir(dir)
	if err != nil || !truncated {
		t.Fatalf("many files: truncated=%v err=%v", truncated, err)
	}
	files, err := UnpackFiles(tb)
	if err != nil || len(files) != MaxFiles {
		t.Fatalf("got %d files err=%v, want %d", len(files), err, MaxFiles)
	}

	big := t.TempDir()
	write(t, filepath.Join(big, "big"), make([]byte, MaxOutput+1))
	write(t, filepath.Join(big, "small"), []byte("s"))
	tb, truncated, err = PackDir(big)
	if err != nil || !truncated {
		t.Fatalf("big file: truncated=%v err=%v", truncated, err)
	}
	files, err = UnpackFiles(tb)
	if err != nil || !reflect.DeepEqual(files, map[string][]byte{"small": []byte("s")}) {
		t.Fatalf("got %q err=%v, want only small", files, err)
	}
}

func TestUnpackRejectsTraversal(t *testing.T) {
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	add := func(h *tar.Header, body string) {
		h.Size = int64(len(body))
		if h.Mode == 0 {
			h.Mode = 0o644
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	add(&tar.Header{Typeflag: tar.TypeReg, Name: "../escape"}, "x")
	add(&tar.Header{Typeflag: tar.TypeReg, Name: "a/../../escape"}, "x")
	add(&tar.Header{Typeflag: tar.TypeReg, Name: "/etc/passwd"}, "x")
	add(&tar.Header{Typeflag: tar.TypeSymlink, Name: "link", Linkname: "/etc/passwd"}, "")
	add(&tar.Header{Typeflag: tar.TypeDir, Name: "dir/"}, "")
	add(&tar.Header{Typeflag: tar.TypeReg, Name: "ok.txt"}, "ok")
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := UnpackFiles(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(files, map[string][]byte{"ok.txt": []byte("ok")}) {
		t.Fatalf("got %q, want only ok.txt", files)
	}
}

func TestScrub(t *testing.T) {
	got := Scrub([]byte("xabcx"), []string{"ab", "", "abc"})
	if string(got) != "x***x" {
		t.Fatalf("got %q, want x***x", got)
	}
}

func write(t *testing.T, name string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, b, 0o644); err != nil {
		t.Fatal(err)
	}
}
