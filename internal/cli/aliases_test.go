package cli

import (
	"testing"
)

func TestUnixAliasesMatchCanonical(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n\nsearchable body\n", false, "")
	addTicket(t, dir, "wc-de34", "beta", "todo", "a1", "# Beta\n", false, "depends: [wc-ab2c]\ntags: [style]\n")

	eq := func(name string, canonical, alias []string) {
		t.Helper()
		cOut, cErrOut, cErr := run(t, app, canonical...)
		aOut, aErrOut, aErr := run(t, app, alias...)
		if (cErr == nil) != (aErr == nil) {
			t.Fatalf("%s: canonical err %v alias err %v", name, cErr, aErr)
		}
		if cErr != nil && aErr != nil && cErr.Error() != aErr.Error() {
			t.Errorf("%s: err %q vs %q", name, cErr.Error(), aErr.Error())
		}
		if cOut != aOut || cErrOut != aErrOut {
			t.Errorf("%s: out/errOut mismatch\ncanonical out=%q errOut=%q\nalias out=%q errOut=%q",
				name, cOut, cErrOut, aOut, aErrOut)
		}
	}

	eq("tk ls", []string{"list", "--scope", "wc"}, []string{"ls", "--scope", "wc"})
	eq("note ls", []string{"note", "list", "--scope", "wc"}, []string{"note", "ls", "--scope", "wc"})
	eq("scope ls", []string{"scope", "list"}, []string{"scope", "ls"})
	eq("scope field ls", []string{"scope", "field", "list", "--scope", "wc"}, []string{"scope", "field", "ls", "--scope", "wc"})
	eq("skill ls", []string{"skill", "list"}, []string{"skill", "ls"})
	eq("search find", []string{"search", "searchable", "--scope", "wc"}, []string{"find", "searchable", "--scope", "wc"})
	eq("search fd", []string{"search", "searchable", "--scope", "wc"}, []string{"fd", "searchable", "--scope", "wc"})
	eq("depends deps", []string{"depends", "wc-de34"}, []string{"deps", "wc-de34"})
	eq("depends dep", []string{"depends", "wc-de34"}, []string{"dep", "wc-de34"})
	eq("meta rm usage", []string{"meta", "remove"}, []string{"meta", "rm"})
	eq("meta rm mutate", []string{"meta", "remove", "wc-de34", "tags", "missing"}, []string{"meta", "rm", "wc-de34", "tags", "missing"})
}
