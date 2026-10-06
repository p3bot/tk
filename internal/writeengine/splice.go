package writeengine

import (
	"strings"

	"github.com/p3bot/tk/internal/bodyedit"
	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/gitstate"
)

// SpliceInput is one H1+body splice. Base is the clobber key from GET.
type SpliceInput struct {
	Scope  string
	Dir    string
	Lookup Lookup
	Title  string
	// Lead is the text above the heading. Empty means the heading stays first.
	Lead string
	Body string
	Base string
}

// Splice replaces the post-fence H1 and body, leaving fence bytes unchanged.
// It never self-commits; durability matches create.
func Splice(deps Deps, in SpliceInput) (Result, error) {
	title, err := bodyedit.NormalizeTitle(in.Title)
	if err != nil {
		return Result{}, &UsageError{Msg: err.Error()}
	}
	if strings.TrimSpace(in.Base) == "" {
		return Result{}, &UsageError{Msg: "splice needs a clobber predicate"}
	}

	sess, err := Begin(deps, in.Scope, in.Dir)
	if err != nil {
		return Result{}, err
	}
	defer sess.Release()

	if err := sess.CheckMidRebase(); err != nil {
		return Result{}, err
	}

	out := Result{Warnings: sess.Warnings()}

	p, err := ResolveWriteRow(deps.DB, in.Scope, in.Lookup)
	if err != nil {
		return out, err
	}

	data, key, err := bodyedit.Snapshot(p.Path)
	if err != nil {
		return out, err
	}
	if key != in.Base {
		return out, &ClobberError{ID: p.ID}
	}

	_, body, present := frontmatter.Split(data)
	if !present {
		return out, &ParseQuarantineError{ID: p.ID, Msg: "no frontmatter fence"}
	}
	fence := data[:len(data)-len(body)]
	file := bodyedit.Replace(fence, title, in.Lead, in.Body)
	if err := AtomicWrite(p.Path, file); err != nil {
		return out, err
	}
	if err := deps.Rec.SyncPaths(in.Scope, WrittenPaths(p.Path, "")); err != nil {
		return out, err
	}

	out.ID = p.ID
	out.OldStatus = p.Status
	out.NewStatus = p.Status
	if sess.AutoCommit && sess.HasRoot {
		out.SyncNeeded = gitstate.SyncNeededReason(ctxOf(deps), deps.StateDir, in.Dir, sess.Root)
	}
	abs, err := absPath(p.Path)
	if err != nil {
		return out, err
	}
	out.Path = abs
	return out, nil
}
