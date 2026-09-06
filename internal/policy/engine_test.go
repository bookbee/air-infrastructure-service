package policy

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bookbee/air-infrastructure-service/internal/constants"
	"github.com/bookbee/air-infrastructure-service/internal/schema"
)

// localPolicyPath is the repo's real policy document, exercised below so a
// change to it that breaks loading fails here rather than at boot.
var localPolicyPath = filepath.Join("..", "..", "deploy", "policy", "policy.local.yaml")

func engine(defaultAllow bool) *Engine {
	rpm := 300
	return New(schema.PolicyDoc{
		DefaultAllow:         defaultAllow,
		DefaultRatePerMinute: 100,
		Services: map[string]schema.ServicePolicy{
			"air-classifier": {
				Resources:     map[string][]string{"redis": {"read", "write"}},
				RatePerMinute: &rpm,
			},
		},
	})
}

func TestExplicitGrantAllows(t *testing.T) {
	p := schema.Principal{Service: "air-classifier"}
	if !engine(false).Decide(p, "redis", constants.ActionRead).Allow {
		t.Fatal("an explicit grant must allow")
	}
}

func TestMissingGrantDeniesWhenDefaultDeny(t *testing.T) {
	p := schema.Principal{Service: "air-classifier"}
	d := engine(false).Decide(p, "postgres", constants.ActionConnect)
	if d.Allow {
		t.Fatal("no grant on postgres must deny under default-deny")
	}
	if !strings.Contains(d.Reason, "postgres") {
		t.Errorf("reason should name the resource, got %q", d.Reason)
	}
}

func TestUnknownServiceDeniedWhenDefaultDeny(t *testing.T) {
	d := engine(false).Decide(schema.Principal{Service: "ghost"}, "redis", constants.ActionRead)
	if d.Allow {
		t.Fatal("an unknown service must deny under default-deny")
	}
}

func TestDefaultAllowOpensTheGate(t *testing.T) {
	d := engine(true).Decide(schema.Principal{Service: "ghost"}, "redis", constants.ActionRead)
	if !d.Allow {
		t.Fatal("default-allow must let an unknown service through")
	}
	if !strings.Contains(d.Reason, "default-allow") {
		t.Errorf("reason should say it was default-allow, got %q", d.Reason)
	}
}

func TestRateOverrideAndDefault(t *testing.T) {
	e := engine(false)
	if got := e.RatePerMinute(schema.Principal{Service: "air-classifier"}); got != 300 {
		t.Errorf("override: got %d, want 300", got)
	}
	if got := e.RatePerMinute(schema.Principal{Service: "ghost"}); got != 100 {
		t.Errorf("default: got %d, want 100", got)
	}
}

func TestCanSeeIsGrantDrivenNotDefaultAllow(t *testing.T) {
	// default-allow is on, but visibility follows explicit grants: a listing
	// reflects what a service is declared to use, not what it could reach.
	e := engine(true)
	p := schema.Principal{Service: "air-classifier"}
	if !e.CanSee(p, "redis") {
		t.Error("a granted resource must be visible")
	}
	if e.CanSee(p, "postgres") {
		t.Error("an ungranted resource must stay invisible even under default-allow")
	}
}

func TestLocalPolicyYAMLLoads(t *testing.T) {
	e, err := Load(localPolicyPath, 120)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !e.Decide(schema.Principal{Service: "air-client"}, "redis", constants.ActionRead).Allow {
		t.Error("air-client should be granted read on redis by the shipped policy")
	}
	if got := e.RatePerMinute(schema.Principal{Service: "air-platform"}); got != 600 {
		t.Errorf("air-platform rate: got %d, want 600", got)
	}
}

func TestMissingPolicyFileDeniesRatherThanFailingTheBoot(t *testing.T) {
	e, err := Load(filepath.Join(t.TempDir(), "absent.yaml"), 120)
	if err != nil {
		t.Fatalf("a missing policy file must not fail the boot: %v", err)
	}
	if e.Decide(schema.Principal{Service: "anyone"}, "redis", constants.ActionRead).Allow {
		t.Error("a missing policy file must deny by default, not open the gate")
	}
	if e.Doc().DefaultRatePerMinute != 120 {
		t.Error("the configured default rate should survive a missing file")
	}
}

func TestRateLimiterAllowsBurstThenLimits(t *testing.T) {
	now := time.Now()
	r := NewRateLimiter(120)
	r.now = func() time.Time { return now }

	for i := range 3 {
		if !r.Check("air-classifier", "redis", 3) {
			t.Fatalf("call %d should be within a limit of 3", i+1)
		}
	}
	if r.Check("air-classifier", "redis", 3) {
		t.Fatal("the 4th call should be limited")
	}

	// Buckets are per (service, resource): another resource is unaffected.
	if !r.Check("air-classifier", "postgres", 3) {
		t.Fatal("a different resource has its own bucket")
	}

	// One token refills in 60/3 = 20s.
	now = now.Add(20 * time.Second)
	if !r.Check("air-classifier", "redis", 3) {
		t.Fatal("a token should have refilled after 20s")
	}
}
