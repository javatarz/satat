// Package entry is the result frame a Kubernetes step pod writes to its log and the worker reads
// back through pods/log. The satat-entry wrapper writes one frame as the pod's only stdout; the
// launcher takes the last complete frame in the log.
//
// A frame is three lines: Begin, the standard base64 of the JSON Result, End. Base64 keeps the
// step's own output, binary or not, from ever looking like a frame boundary.
package entry

import (
	"archive/tar"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	// MaxStream is the most of stdout, and separately of stderr, a frame carries.
	MaxStream = 1 << 20
	// MaxOutput is the most file content, in bytes, the output tar carries.
	MaxOutput = 4 << 20
	// MaxFiles is the most files the output tar carries.
	MaxFiles = 64

	Begin = "---SATAT-RESULT-BEGIN v1---"
	End   = "---SATAT-RESULT-END v1---"
)

// Result is what one step left behind, with the same meanings as launcher.Result.
type Result struct {
	ExitCode      int    `json:"exit_code"`
	OOMKilled     bool   `json:"oom_killed"`
	TimedOut      bool   `json:"timed_out"`
	Stdout        []byte `json:"stdout"`
	Stderr        []byte `json:"stderr"`
	LogsTruncated bool   `json:"logs_truncated"`
	// Output is a tar of the regular files the step left in $SATAT_OUTPUT (see PackDir).
	Output         []byte `json:"output"`
	FilesTruncated bool   `json:"files_truncated"`
}

// Write writes r as one frame.
func Write(w io.Writer, r Result) error {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode result: %w", err)
	}
	_, err = fmt.Fprintf(w, "%s\n%s\n%s\n", Begin, base64.StdEncoding.EncodeToString(b), End)
	return err
}

// Parse returns the last complete frame in log. ok is false when there is none. A frame that is
// complete but cannot be decoded is an error.
func Parse(log []byte) (r Result, ok bool, err error) {
	lines := strings.Split(string(log), "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	payload := ""
	for i := 0; i+2 < len(lines); i++ {
		if lines[i] == Begin && lines[i+2] == End {
			payload, ok = lines[i+1], true
		}
	}
	if !ok {
		return Result{}, false, nil
	}
	b, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return Result{}, false, fmt.Errorf("decode result frame: %w", err)
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return Result{}, false, fmt.Errorf("decode result frame: %w", err)
	}
	return r, true, nil
}

// PackDir tars the regular files under dir, named by slash-separated paths relative to it.
// Symlinks, directories and other entries are skipped. Files past MaxFiles, or that would take
// the content past MaxOutput, are dropped and truncated is set.
func PackDir(dir string) (tb []byte, truncated bool, err error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	var count int
	var total int64
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if count >= MaxFiles || total+info.Size() > MaxOutput {
			truncated = true
			return nil
		}
		b, err := readAtMost(p, info.Size())
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg,
			Name:     filepath.ToSlash(rel),
			Mode:     0o644,
			Size:     int64(len(b)),
			ModTime:  time.Unix(0, 0),
		}); err != nil {
			return err
		}
		if _, err := tw.Write(b); err != nil {
			return err
		}
		count++
		total += int64(len(b))
		return nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("pack %s: %w", dir, err)
	}
	if err := tw.Close(); err != nil {
		return nil, false, fmt.Errorf("pack %s: %w", dir, err)
	}
	return buf.Bytes(), truncated, nil
}

// readAtMost reads up to n bytes of a file, so one that grows after it was measured cannot take
// the output past its cap.
func readAtMost(name string, n int64) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, n))
}

// UnpackFiles reads the regular files out of a tar made by PackDir, keyed by relative path.
// Entries that are not regular files, or whose path is absolute or leaves the root, are skipped.
// The MaxFiles and MaxOutput caps apply again; entries past them are dropped.
func UnpackFiles(tb []byte) (map[string][]byte, error) {
	files := map[string][]byte{}
	var total int64
	tr := tar.NewReader(bytes.NewReader(tb))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read output: %w", err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		rel := path.Clean(h.Name)
		if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
			continue
		}
		if len(files) >= MaxFiles || total+h.Size > MaxOutput {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(tr, h.Size))
		if err != nil {
			return nil, fmt.Errorf("read output %s: %w", rel, err)
		}
		total += int64(len(b))
		files[rel] = b
	}
}

// Scrub replaces every non-empty secret in b with ***, longest first so a secret that contains
// another is replaced whole.
func Scrub(b []byte, secrets []string) []byte {
	sorted := slices.Clone(secrets)
	slices.SortFunc(sorted, func(x, y string) int { return len(y) - len(x) })
	for _, s := range sorted {
		if s != "" {
			b = bytes.ReplaceAll(b, []byte(s), []byte("***"))
		}
	}
	return b
}
