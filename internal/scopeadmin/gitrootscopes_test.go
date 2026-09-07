package scopeadmin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/gitroot"
	"github.com/p3bot/tk/internal/token"
)

func TestGitRootScopesLiveDropsGoneDirs(t *testing.T) {
	h := newHarness(t)
	repo := filepath.Join(t.TempDir(), "repo")
	gitInit(t, repo)
	dirA := filepath.Join(repo, "aa")
	dirB := filepath.Join(repo, "bb")
	if _, err := h.admin.Init(InitParams{Dir: dirA, Name: "aa", CodeRoot: dirA, CodeRootGiven: true}); err != nil {
		t.Fatalf("init aa: %v", err)
	}
	if _, err := h.admin.Init(InitParams{Dir: dirB, Name: "bb", CodeRoot: dirB, CodeRootGiven: true}); err != nil {
		t.Fatalf("init bb: %v", err)
	}
	root, ok := gitroot.RepoRoot(dirA)
	if !ok {
		t.Fatal("expected git-root")
	}

	live := GitRootScopes(h.reg(t), root)
	if len(live) != 2 || live[0].Name != "aa" || live[1].Name != "bb" {
		t.Fatalf("live = %+v want aa, bb in name order", live)
	}

	if err := os.RemoveAll(dirB); err != nil {
		t.Fatal(err)
	}
	reg := h.reg(t)
	dropped := GitRootScopes(reg, root)
	if len(dropped) != 1 || dropped[0].Name != "aa" {
		t.Fatalf("gone dir must drop from live listing, got %+v", dropped)
	}
	_, err := GitRootScopesRefuseUnreachable(reg, root)
	if err == nil {
		t.Fatal("refuse-unreachable must fail on gone sibling")
	}
	if !strings.Contains(err.Error(), token.UnreachableScope) {
		t.Errorf("want unreachable_scope, got %v", err)
	}
}
