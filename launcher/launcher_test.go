package launcher

import (
	"regexp"
	"testing"
)

func TestCacheScopeIsAFixedShapePerRepo(t *testing.T) {
	a, b := CacheScope("javatarz/satat"), CacheScope("javatarz/satat-sandbox")
	if !regexp.MustCompile(`^[a-z0-9]{32}$`).MatchString(a) {
		t.Fatalf("scope %q is not 32 lower-case hex characters", a)
	}
	if a == b {
		t.Error("two repos share a cache scope")
	}
	if CacheScope("Javatarz/Satat") != a {
		t.Error("the scope must not depend on the case of the repo name")
	}
	if CacheScope("javatarz/satat") != a {
		t.Error("the scope must be stable")
	}
}
