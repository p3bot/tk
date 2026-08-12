package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/agentdex"

	"github.com/p3bot/tk/internal/skill"
)

// Minimal agentdex catalog schema (mirrors agentdex catalog/schema.cue).
const skillCatalogSchema = `package catalog

import "struct"

#KnownAgent: {
	name:         string & !=""
	bin:          string & !=""
	description?: string
	config: {
		global: string & !=""
		local?: string & !=""
	}
	skills?: {
		global?: #SkillsScope
		local?:  #SkillsScope
		struct.MinFields(1)
	}
	version?: {
		args: [string, ...string]
		pattern?: string
	}
	agnostic: bool | *false
	if !agnostic {
		provider: [string, ...string]
	}
	homepage?: string
}

#SkillsScope: {
	agents?:       string & !=""
	native?:       string & !=""
	alternatives?: [string & !="", ...(string & !="")]
	struct.MinFields(1)
}

agents: [=~"^[a-z0-9]+(-[a-z0-9]+)*$"]: #KnownAgent
`

func writeSkillCatalog(t *testing.T, agentsBody string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join("cue.mod", "module.cue"), "module: \"github.com/p3bot/agentdex/catalog@v1\"\nlanguage: {\n\tversion: \"v0.16.0\"\n}\n")
	write("schema.cue", skillCatalogSchema)
	write("agents.cue", "package catalog\n\n"+agentsBody+"\n")
	return dir
}

func skillFixtureBins(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho v1.0.0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func skillEnvHome(home string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		if k == "HOME" {
			return home, true
		}
		return "", false
	}
}

// skillApp wires a fixture catalog: alpha (native only), gamma (shared agents + alt),
// delta (shared agents), beta (no skills). Installed: alpha + gamma when their bins are present.
func skillApp(t *testing.T, home, wd string, installed ...string) *App {
	t.Helper()
	const (
		binAlpha = "tk-fixture-alpha"
		binBeta  = "tk-fixture-beta"
		binGamma = "tk-fixture-gamma"
		binDelta = "tk-fixture-delta"
	)
	body := `
agents: "alpha-cli": {
	name: "Alpha CLI"
	bin:  "` + binAlpha + `"
	config: {global: "~/.alpha", local: ".alpha"}
	skills: {
		global: {native: "~/.alpha/skills"}
		local:  {native: ".alpha/skills"}
	}
	provider: ["anthropic"]
}
agents: "beta-tool": {
	name: "Beta Tool"
	bin:  "` + binBeta + `"
	config: {global: "~/.beta"}
	provider: ["openai"]
}
agents: "gamma-agent": {
	name: "Gamma Agent"
	bin:  "` + binGamma + `"
	config: {global: "~/.gamma", local: ".gamma"}
	skills: {
		global: {
			agents: "~/.agents/skills"
			alternatives: ["~/.claude/skills"]
		}
		local: {
			agents: ".agents/skills"
			alternatives: [".claude/skills"]
		}
	}
	provider: ["google"]
}
agents: "delta-agent": {
	name: "Delta Agent"
	bin:  "` + binDelta + `"
	config: {global: "~/.delta", local: ".delta"}
	skills: {
		global: {agents: "~/.agents/skills"}
		local:  {agents: ".agents/skills"}
	}
	provider: ["openai"]
}
agents: "epsilon-alt": {
	name: "Epsilon Alt Only"
	bin:  "tk-fixture-epsilon"
	config: {global: "~/.epsilon", local: ".epsilon"}
	skills: {
		global: {alternatives: ["~/.epsilon/skills"]}
		local:  {alternatives: [".epsilon/skills"]}
	}
	provider: ["openai"]
}
`
	catalogDir := writeSkillCatalog(t, body)
	binDir := skillFixtureBins(t, installed...)
	look := func(file string) (string, error) {
		for _, n := range installed {
			if file == n {
				return filepath.Join(binDir, n), nil
			}
		}
		return "", exec.ErrNotFound
	}
	// Map installed names to bins: pass basename list
	// installed should be full bin names like tk-fixture-alpha
	app := newApp(t)
	app.AgentdexOpts = []agentdex.Option{
		agentdex.WithCatalogDir(catalogDir),
		agentdex.WithCacheDir(t.TempDir()),
		agentdex.WithEnvLookup(skillEnvHome(home)),
		agentdex.WithWorkingDir(wd),
		agentdex.WithLookPath(look),
		agentdex.WithSearchDirs(binDir),
	}
	return app
}

func TestSkillPrintsContractNoScope(t *testing.T) {
	app := newApp(t)
	out, errOut, err := run(t, app, "skill")
	if err != nil {
		t.Fatalf("tk skill: %v", err)
	}
	if errOut != "" {
		t.Errorf("skill must write nothing to stderr, got %q", errOut)
	}
	if out != skill.Text() {
		t.Fatalf("stdout is not skill.Text() (got %d bytes, want %d)", len(out), len(skill.Text()))
	}
	// skills is the plural alias (mirrors scope/scopes, field/fields).
	aliasOut, aliasErrOut, aliasErr := run(t, app, "skills")
	if aliasErr != nil {
		t.Fatalf("tk skills: %v", aliasErr)
	}
	if aliasErrOut != "" {
		t.Errorf("skills must write nothing to stderr, got %q", aliasErrOut)
	}
	if aliasOut != out {
		t.Errorf("skills alias stdout differs from skill")
	}
	for _, h := range skill.RequiredHeadings() {
		if !strings.Contains(out, "## "+h+"\n") {
			t.Errorf("missing section %q", h)
		}
	}
	if entries, _ := os.ReadDir(app.ConfigDir); len(entries) != 0 {
		t.Errorf("skill must not write config dir, found %v", names(entries))
	}
	if entries, _ := os.ReadDir(app.StateDir); len(entries) != 0 {
		t.Errorf("skill must not write state dir, found %v", names(entries))
	}
}

func TestSkillInstallDefaultPrimary(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	app := skillApp(t, home, wd, "tk-fixture-alpha", "tk-fixture-gamma")

	out, errOut, err := run(t, app, "skill", "install")
	if err != nil {
		t.Fatalf("install: %v\nstderr=%s", err, errOut)
	}
	// alpha Primary = native ~/.alpha/skills; gamma Primary = agents ~/.agents/skills
	// Printed alphabetically: .agents before .alpha
	alphaFile := filepath.Join(home, ".alpha", "skills", "tk", "SKILL.md")
	gammaFile := filepath.Join(home, ".agents", "skills", "tk", "SKILL.md")
	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("want 2 install paths, got %q", out)
	}
	if lines[0] != gammaFile || lines[1] != alphaFile {
		t.Fatalf("paths = %v want alphabetical [%s, %s]", lines, gammaFile, alphaFile)
	}
	for _, p := range []string{alphaFile, gammaFile} {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != skill.Text() {
			t.Errorf("%s content mismatch", p)
		}
	}
}

func TestSkillInstallNamedNativeElseShared(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	// Only gamma installed for default-set elsewhere; install delta by name (!Found OK)
	app := skillApp(t, home, wd, "tk-fixture-gamma")

	out, _, err := run(t, app, "skill", "install", "delta-agent")
	if err != nil {
		t.Fatal(err)
	}
	// delta has no native → Shared ~/.agents/skills
	want := filepath.Join(home, ".agents", "skills", "tk", "SKILL.md")
	if strings.TrimSpace(out) != want {
		t.Fatalf("out = %q want %q", out, want)
	}
	// alpha by name uses native
	out, _, err = run(t, app, "skill", "install", "alpha-cli")
	if err != nil {
		t.Fatal(err)
	}
	wantAlpha := filepath.Join(home, ".alpha", "skills", "tk", "SKILL.md")
	if strings.TrimSpace(out) != wantAlpha {
		t.Fatalf("alpha out = %q want %q", out, wantAlpha)
	}
}

func TestSkillInstallLocal(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	app := skillApp(t, home, wd, "tk-fixture-alpha")

	out, _, err := run(t, app, "skill", "install", "--local")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(wd, ".alpha", "skills", "tk", "SKILL.md")
	if strings.TrimSpace(out) != want {
		t.Fatalf("out = %q want %q", out, want)
	}
	// global not written
	if _, err := os.Stat(filepath.Join(home, ".alpha", "skills", "tk", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("global should not be written with --local")
	}
}

func TestSkillInstallEmptyDefaultSet(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	app := skillApp(t, home, wd) // nothing installed
	_, _, err := run(t, app, "skill", "install")
	if ExitCodeFromError(err) != exitUsage {
		t.Fatalf("exit = %d want %d (err=%v)", ExitCodeFromError(err), exitUsage, err)
	}
}

func TestSkillUninstallEmptyDefaultSet(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	app := skillApp(t, home, wd)
	_, _, err := run(t, app, "skill", "uninstall")
	if ExitCodeFromError(err) != exitUsage {
		t.Fatalf("exit = %d want %d (err=%v)", ExitCodeFromError(err), exitUsage, err)
	}
}

func TestSkillInstallUnknownAndNoSkills(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	app := skillApp(t, home, wd, "tk-fixture-alpha")

	_, _, err := run(t, app, "skill", "install", "no-such-agent")
	if ExitCodeFromError(err) != exitUsage {
		t.Fatalf("unknown: exit = %d want 2 err=%v", ExitCodeFromError(err), err)
	}
	_, _, err = run(t, app, "skill", "install", "beta-tool")
	if ExitCodeFromError(err) != exitUsage {
		t.Fatalf("no-skills: exit = %d want 2 err=%v", ExitCodeFromError(err), err)
	}
}

func TestSkillInstallNamedNoWritablePath(t *testing.T) {
	// epsilon-alt has skills concept via alternatives only; named rule is
	// Native else Shared — both empty → exit 2, no write.
	home := t.TempDir()
	wd := t.TempDir()
	app := skillApp(t, home, wd, "tk-fixture-alpha")

	_, _, err := run(t, app, "skill", "install", "epsilon-alt")
	if ExitCodeFromError(err) != exitUsage {
		t.Fatalf("exit = %d want %d (err=%v)", ExitCodeFromError(err), exitUsage, err)
	}
	if err == nil || !strings.Contains(err.Error(), "writable") {
		t.Fatalf("want writable-path error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".epsilon", "skills", "tk", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("named install must not write when Native and Shared are empty")
	}
}

func TestSkillInstallDedupeShared(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	// Install both gamma and delta by name → same shared path once
	app := skillApp(t, home, wd)
	out, _, err := run(t, app, "skill", "install", "gamma-agent", "delta-agent")
	if err != nil {
		t.Fatal(err)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 1 {
		t.Fatalf("want 1 path for shared de-dupe, got %q", out)
	}
	want := filepath.Join(home, ".agents", "skills", "tk", "SKILL.md")
	if lines[0] != want {
		t.Fatalf("path = %q want %q", lines[0], want)
	}
}

func TestSkillList(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	app := skillApp(t, home, wd, "tk-fixture-alpha", "tk-fixture-gamma", "tk-fixture-delta")

	// empty list when nothing installed on disk
	out, errOut, err := run(t, app, "skill", "list")
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Fatalf("empty inventory want empty stdout, got %q", out)
	}
	if !strings.Contains(errOut, "not installed") {
		t.Fatalf("empty inventory want stderr note, got %q", errOut)
	}

	if _, _, err := run(t, app, "skill", "install"); err != nil {
		t.Fatal(err)
	}
	out, _, err = run(t, app, "skill", "list")
	if err != nil {
		t.Fatal(err)
	}
	// default set is all three installed; alpha native + shared (gamma+delta claim)
	// alphabetical: .agents before .alpha
	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("list lines = %q", out)
	}
	shared := filepath.Join(home, ".agents", "skills", "tk", "SKILL.md")
	alpha := filepath.Join(home, ".alpha", "skills", "tk", "SKILL.md")
	parts0 := strings.SplitN(lines[0], "\t", 2)
	parts1 := strings.SplitN(lines[1], "\t", 2)
	if parts0[0] != shared || parts1[0] != alpha {
		t.Fatalf("list order = %v want alphabetical [%s, %s]", lines, shared, alpha)
	}
	// agent ids within a line are sorted (JoinAgents)
	if len(parts0) < 2 || parts0[1] != "delta-agent,gamma-agent" {
		t.Fatalf("shared agents = %q want delta-agent,gamma-agent", parts0)
	}
}

func TestSkillListEmptyDefaultSet(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	app := skillApp(t, home, wd) // no bins
	out, errOut, err := run(t, app, "skill", "list")
	if err != nil {
		t.Fatalf("list empty set: %v", err)
	}
	if out != "" {
		t.Fatalf("want empty stdout, got %q", out)
	}
	if !strings.Contains(errOut, "not installed") {
		t.Fatalf("want stderr note, got %q", errOut)
	}
}

func TestSkillListLocal(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	app := skillApp(t, home, wd, "tk-fixture-alpha")
	if _, _, err := run(t, app, "skill", "install", "--local"); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, app, "skill", "list", "--local")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(wd, ".alpha", "skills", "tk", "SKILL.md")
	if !strings.Contains(out, want) {
		t.Fatalf("list --local missing %s in %q", want, out)
	}
	// Global inventory empty (nothing written at global roots).
	out, errOut, err := run(t, app, "skill", "list")
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Fatalf("global list should be empty stdout after local-only install, got %q", out)
	}
	if !strings.Contains(errOut, "not installed") {
		t.Fatalf("global list should note not installed, got %q", errOut)
	}
}

func TestSkillUninstallLocal(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	app := skillApp(t, home, wd, "tk-fixture-alpha")
	if _, _, err := run(t, app, "skill", "install", "--local"); err != nil {
		t.Fatal(err)
	}
	localDir := filepath.Join(wd, ".alpha", "skills", "tk")
	out, _, err := run(t, app, "skill", "uninstall", "--local")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "removed\t"+localDir) {
		t.Fatalf("want removed local, got %q", out)
	}
	if _, err := os.Stat(localDir); !os.IsNotExist(err) {
		t.Fatal("local skill dir should be gone")
	}
}

func TestSkillUninstallMultiTenantKeep(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	app := skillApp(t, home, wd, "tk-fixture-gamma", "tk-fixture-delta")
	if _, _, err := run(t, app, "skill", "install"); err != nil {
		t.Fatal(err)
	}
	sharedDir := filepath.Join(home, ".agents", "skills", "tk")

	// Uninstall only gamma: delta still claims shared path → keep
	out, _, err := run(t, app, "skill", "uninstall", "gamma-agent")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "kept\t"+sharedDir) {
		t.Fatalf("want kept shared, got %q", out)
	}
	if !strings.Contains(out, "delta-agent") {
		t.Fatalf("want delta blocker, got %q", out)
	}
	if _, err := os.Stat(filepath.Join(sharedDir, "SKILL.md")); err != nil {
		t.Fatal("shared skill should remain")
	}

	// Uninstall both → removed
	out, _, err = run(t, app, "skill", "uninstall", "gamma-agent", "delta-agent")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "removed\t"+sharedDir) {
		t.Fatalf("want removed, got %q", out)
	}
	if _, err := os.Stat(sharedDir); !os.IsNotExist(err) {
		t.Fatal("shared dir should be gone")
	}
}

func TestSkillUninstallMultiTenantAbsent(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	// Both installed, nothing written on disk.
	app := skillApp(t, home, wd, "tk-fixture-gamma", "tk-fixture-delta")
	sharedDir := filepath.Join(home, ".agents", "skills", "tk")
	out, _, err := run(t, app, "skill", "uninstall", "gamma-agent")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "absent\t"+sharedDir) {
		t.Fatalf("want absent when blocked and missing, got %q", out)
	}
	if !strings.Contains(out, "delta-agent") {
		t.Fatalf("want delta blocker on absent line, got %q", out)
	}
	if strings.Contains(out, "kept\t"+sharedDir) {
		t.Fatalf("must not report kept for missing skill, got %q", out)
	}
}

func TestSkillUninstallPurity(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	app := skillApp(t, home, wd, "tk-fixture-alpha")
	if _, _, err := run(t, app, "skill", "install", "alpha-cli"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".alpha", "skills", "tk")
	if err := os.WriteFile(filepath.Join(dir, "extra.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, app, "skill", "uninstall", "alpha-cli")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "kept\t"+dir) {
		t.Fatalf("want kept for extra, got %q", out)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("dir should remain")
	}
}

func TestSkillUninstallEditedBodyStillRemoves(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	app := skillApp(t, home, wd, "tk-fixture-alpha")
	if _, _, err := run(t, app, "skill", "install", "alpha-cli"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".alpha", "skills", "tk", "SKILL.md")
	if err := os.WriteFile(path, []byte("---\nname: tk\ndescription: hand edit\n---\n\n# hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, app, "skill", "uninstall", "alpha-cli")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".alpha", "skills", "tk")
	if !strings.Contains(out, "removed\t"+dir) {
		t.Fatalf("want removed, got %q", out)
	}
}

func TestSkillCatalogInvalid(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	// Invalid module: agents body that fails schema (empty name)
	bad := writeSkillCatalog(t, `agents: "x": { name: "", bin: "y", config: {global: "~/.x"}, provider: ["openai"] }`)
	app := newApp(t)
	app.AgentdexOpts = []agentdex.Option{
		agentdex.WithCatalogDir(bad),
		agentdex.WithCacheDir(t.TempDir()),
		agentdex.WithEnvLookup(skillEnvHome(home)),
		agentdex.WithWorkingDir(wd),
		agentdex.WithLookPath(func(string) (string, error) { return "", exec.ErrNotFound }),
	}
	_, _, err := run(t, app, "skill", "install")
	if err == nil {
		t.Fatal("expected catalog failure")
	}
	if ExitCodeFromError(err) == exitUsage {
		t.Fatalf("catalog failure should not be usage exit, got %d", ExitCodeFromError(err))
	}
	if !strings.Contains(err.Error(), "tk skill") {
		t.Fatalf("message should point at manual install: %v", err)
	}
}

func TestSkillInstallFamilyInHelp(t *testing.T) {
	app := newApp(t)
	out, _, err := run(t, app, "skill", "--help")
	if err != nil {
		t.Fatalf("help: %v", err)
	}
	for _, sub := range []string{"install", "list", "uninstall"} {
		if !strings.Contains(out, sub) {
			t.Errorf("skill help missing %q", sub)
		}
	}
}

func TestScopeInitWritesNoAgentsMD(t *testing.T) {
	app := newApp(t)
	dir := filepath.Join(t.TempDir(), "home")
	if _, _, err := run(t, app, "scope", "init", dir, "--name", "home"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("scope init must not write AGENTS.md, stat err=%v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		switch e.Name() {
		case "tk.cue", ".gitignore":
		default:
			t.Errorf("unexpected init artefact %q", e.Name())
		}
	}
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func names(entries []os.DirEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name()
	}
	return out
}
