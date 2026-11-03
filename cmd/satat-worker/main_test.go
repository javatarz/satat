package main

import (
	"regexp"
	"testing"
)

func TestWorkerNameFitsHatchet(t *testing.T) {
	hatchetName := regexp.MustCompile(`^[a-zA-Z0-9.\-_]+$`)
	for host, want := range map[string]string{
		"3f2a9c1d0b7e":        "satat-worker-3f2a9c1d0b7e",
		"Amaan’s-MacBook-Pro": "satat-worker-Amaan-s-MacBook-Pro",
		"büro box.local":      "satat-worker-b-ro-box.local",
	} {
		got := workerName(host)
		if got != want {
			t.Errorf("workerName(%q) = %q, want %q", host, got, want)
		}
		if !hatchetName.MatchString(got) {
			t.Errorf("workerName(%q) = %q, which Hatchet refuses", host, got)
		}
	}
}
