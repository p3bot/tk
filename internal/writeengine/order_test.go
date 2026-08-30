package writeengine

import (
	"errors"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/order"
)

func TestOrderBoundsFirstLastBeforeAfter(t *testing.T) {
	e := newPlainEnv(t, "wc", "name: \"wc\"\nautoCommit: false\n")
	addTicketExtra(t, e.dir, "wc-aa22", "a0", "")
	addTicketExtra(t, e.dir, "wc-bb33", "a1", "")
	addTicketExtra(t, e.dir, "wc-cc44", "a2", "")

	_, err := Order(e.deps, OrderInput{
		Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-cc44"), Dest: Dest{First: true},
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	c := parseTicket(t, e.dir+"/wc-cc44-work.md").Order
	a := parseTicket(t, e.dir+"/wc-aa22-work.md").Order
	if c >= a {
		t.Errorf("--first: %q should be < %q", c, a)
	}

	_, err = Order(e.deps, OrderInput{
		Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-aa22"), Dest: Dest{Last: true},
	})
	if err != nil {
		t.Fatalf("last: %v", err)
	}
	a = parseTicket(t, e.dir+"/wc-aa22-work.md").Order
	b := parseTicket(t, e.dir+"/wc-bb33-work.md").Order
	if a <= b {
		t.Errorf("--last: %q should be > %q", a, b)
	}

	_, err = Order(e.deps, OrderInput{
		Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-bb33"),
		Dest: Dest{Before: fullLookup("wc-cc44")},
	})
	if err != nil {
		t.Fatalf("before: %v", err)
	}
	b = parseTicket(t, e.dir+"/wc-bb33-work.md").Order
	c = parseTicket(t, e.dir+"/wc-cc44-work.md").Order
	if b >= c {
		t.Errorf("--before: %q should be < %q", b, c)
	}

	_, err = Order(e.deps, OrderInput{
		Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-bb33"),
		Dest: Dest{After: fullLookup("wc-cc44")},
	})
	if err != nil {
		t.Fatalf("after: %v", err)
	}
	b = parseTicket(t, e.dir+"/wc-bb33-work.md").Order
	c = parseTicket(t, e.dir+"/wc-cc44-work.md").Order
	if b <= c {
		t.Errorf("--after: %q should be > %q", b, c)
	}
}

func TestOrderNoLegalBetweenAndSelf(t *testing.T) {
	e := newPlainEnv(t, "wc", "name: \"wc\"\nautoCommit: false\n")
	addTicketExtra(t, e.dir, "wc-aa22", "a0", "")
	addTicketExtra(t, e.dir, "wc-bb33", "a0", "")
	addTicketExtra(t, e.dir, "wc-cc44", "a1", "")

	_, err := Order(e.deps, OrderInput{
		Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-cc44"),
		Dest: Dest{After: fullLookup("wc-aa22")},
	})
	var nlo *NoLegalOrderError
	if !errors.As(err, &nlo) {
		t.Fatalf("want no-legal-between, got %v", err)
	}
	if !errors.Is(err, order.ErrEqualKeys) {
		t.Errorf("unwrap: %v", err)
	}
	if !strings.Contains(err.Error(), "re-space with tk doctor") {
		t.Errorf("message: %v", err)
	}

	_, err = Order(e.deps, OrderInput{
		Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-aa22"),
		Dest: Dest{Before: fullLookup("wc-aa22")},
	})
	var use *UsageError
	if !errors.As(err, &use) || !strings.Contains(use.Msg, "relative to itself") {
		t.Errorf("self: %v", err)
	}

	_, err = Order(e.deps, OrderInput{
		Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-aa22"), Dest: Dest{},
	})
	if !errors.As(err, &use) || !strings.Contains(use.Msg, "exactly one") {
		t.Errorf("no dest: %v", err)
	}
}

func TestOrderSelfCommit(t *testing.T) {
	e, repo := initAutoCommitRepo(t, "od")
	addTicketExtra(t, e.dir, "od-aa22", "a0", "")
	addTicketExtra(t, e.dir, "od-bb33", "a1", "")
	if _, err := Order(e.deps, OrderInput{
		Scope: "od", Dir: e.dir, Lookup: fullLookup("od-bb33"), Dest: Dest{First: true},
	}); err != nil {
		t.Fatalf("order: %v", err)
	}
	log := gitLog(t, repo)
	if len(log) < 1 || !strings.Contains(log[0], "order") {
		t.Errorf("self-commit log=%v", log)
	}
}
