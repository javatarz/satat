package deploy

import (
	"strings"
	"testing"
)

func TestFromEnv(t *testing.T) {
	getenv := func(name string) (string, bool) {
		return map[string]string{"A": "placeholder-a", "B": "placeholder-b"}[name], name != "MISSING"
	}
	got, err := FromEnv([]string{"A", "B"}, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if got["A"] != "placeholder-a" || got["B"] != "placeholder-b" || len(got) != 2 {
		t.Errorf("got %v", got)
	}
	_, err = FromEnv([]string{"A", "MISSING", "EMPTY"}, getenv)
	if err == nil || !strings.Contains(err.Error(), "MISSING: environment variable is not set") ||
		!strings.Contains(err.Error(), "EMPTY: environment variable is empty") {
		t.Fatalf("want errors naming MISSING and EMPTY, got %v", err)
	}
	if strings.Contains(err.Error(), "placeholder-") {
		t.Errorf("error holds a value: %v", err)
	}
}
