package pipeline

import "testing"

// The pusher reads an optional top-level `protected:` key from the same file (#17). The file stays
// version 1, so ParseCommands, and the readiness check built on it, must ignore the key.
func TestParseCommandsIgnoresTheProtectedSection(t *testing.T) {
	in := goodCommands + "protected:\n  add: [justfile]\n  allow: [.editorconfig]\n"
	cmds, err := ParseCommands([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if cmds["setup"] != "just setup" || cmds["test"] != "go test ./..." || len(cmds) != 3 {
		t.Errorf("commands = %v", cmds)
	}
}
