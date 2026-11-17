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

func TestTokenTenantReadsTheSubject(t *testing.T) {
	// header.payload.signature, with payload {"sub":"707d0855-80ab-4e1f-a156-f1c4546cbf52"}.
	tok := "eyJhbGciOiJub25lIn0.eyJzdWIiOiI3MDdkMDg1NS04MGFiLTRlMWYtYTE1Ni1mMWM0NTQ2Y2JmNTIifQ.sig"
	if got := tokenTenant(tok); got != "707d0855-80ab-4e1f-a156-f1c4546cbf52" {
		t.Errorf("tenant = %q", got)
	}
	for _, bad := range []string{"", "not-a-jwt", "a.!!!.c"} {
		if got := tokenTenant(bad); got != "" {
			t.Errorf("tokenTenant(%q) = %q, want empty", bad, got)
		}
	}
}
