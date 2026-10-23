package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestResolveUsesOpAndEnv(t *testing.T) {
	read := func(_ context.Context, ref string) (string, error) {
		if ref != "op://vault/item/field" {
			t.Errorf("read got ref %q", ref)
		}
		return "from-op", nil
	}
	getenv := func(name string) (string, bool) {
		if name == "B" {
			return "from-env", true
		}
		return "", false
	}
	got, err := Resolve(context.Background(), map[string]string{"A": "op://vault/item/field", "B": "env"}, read, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if got["A"] != "from-op" || got["B"] != "from-env" || len(got) != 2 {
		t.Errorf("got %v", got)
	}
}

func TestResolveNamesTheMissingEnv(t *testing.T) {
	getenv := func(string) (string, bool) { return "", false }
	_, err := Resolve(context.Background(), map[string]string{"MISSING_TOKEN": "env"}, nil, getenv)
	if err == nil || !strings.Contains(err.Error(), "MISSING_TOKEN") {
		t.Fatalf("want error naming MISSING_TOKEN, got %v", err)
	}
}

func TestResolveRejectsEmptyValue(t *testing.T) {
	read := func(context.Context, string) (string, error) { return "", nil }
	_, err := Resolve(context.Background(), map[string]string{"EMPTY": "op://v/i/f"}, read, nil)
	if err == nil || !strings.Contains(err.Error(), "EMPTY") {
		t.Fatalf("want error naming EMPTY, got %v", err)
	}
}

func TestResolveErrorHidesValues(t *testing.T) {
	const fake = "s3cr3t-value-123"
	read := func(context.Context, string) (string, error) {
		return "", errors.New("op said: " + fake)
	}
	_, err := Resolve(context.Background(), map[string]string{"TOKEN": "op://v/i/f"}, read, nil)
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), fake) {
		t.Errorf("error leaks the value: %v", err)
	}
	if !strings.Contains(err.Error(), "TOKEN") {
		t.Errorf("error should name TOKEN: %v", err)
	}
}
