package eval

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// RanTestsCheck passes only when the agent ran a test command and it succeeded. A run that skipped
// the tests, or ran them and saw them fail, is flagged even if the change happens to pass the
// held-out tests.
type RanTestsCheck struct {
	// Commands are regular expressions; a Bash command matching any of them counts as a test run.
	Commands []string `yaml:"commands"`
}

// ranTests reports whether the stream-json transcript holds a Bash tool_use whose command matches
// one of the patterns and whose tool_result was not an error. The two are paired by tool_use_id.
// Lines of unknown shape are ignored; so is anything that does not parse.
func ranTests(transcript []byte, patterns []*regexp.Regexp) (bool, string) {
	calls := map[string]string{} // tool_use_id -> matching Bash command awaiting its result
	var ran, failed []string
	started := 0

	sc := bufio.NewScanner(bytes.NewReader(transcript))
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		var ev struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		var blocks []struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			Name      string `json:"name"`
			ToolUseID string `json:"tool_use_id"`
			IsError   bool   `json:"is_error"`
			Input     struct {
				Command string `json:"command"`
			} `json:"input"`
		}
		// content is a plain string for text-only messages; those carry no tool blocks.
		if json.Unmarshal(ev.Message.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			switch {
			case ev.Type == "assistant" && b.Type == "tool_use" && b.Name == "Bash" && b.ID != "":
				for _, p := range patterns {
					if p.MatchString(b.Input.Command) {
						calls[b.ID] = b.Input.Command
						started++
						break
					}
				}
			case ev.Type == "user" && b.Type == "tool_result":
				cmd, ok := calls[b.ToolUseID]
				if !ok {
					continue
				}
				delete(calls, b.ToolUseID)
				if b.IsError {
					failed = append(failed, cmd)
				} else {
					ran = append(ran, cmd)
				}
			}
		}
	}
	switch {
	case len(ran) > 0:
		return true, fmt.Sprintf("ran %q successfully", oneLine(ran[len(ran)-1]))
	case len(failed) > 0:
		return false, fmt.Sprintf("ran the tests but they failed: %q", oneLine(failed[len(failed)-1]))
	case started > 0:
		return false, "started a test command but the transcript has no result for it"
	}
	return false, "the agent never ran the tests"
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 120 {
		s = string(r[:120]) + "…"
	}
	return s
}

func compileAll(exprs []string) ([]*regexp.Regexp, error) {
	var out []*regexp.Regexp
	for _, e := range exprs {
		re, err := regexp.Compile(e)
		if err != nil {
			return nil, fmt.Errorf("ran_tests command %q: %w", e, err)
		}
		out = append(out, re)
	}
	return out, nil
}
