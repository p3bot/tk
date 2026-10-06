package design

import (
	"fmt"
	"strings"

	"github.com/p3bot/tk/internal/bodyedit"
	"github.com/p3bot/tk/internal/frontmatter"
)

// SpliceInput is one H1 and body replacement. Base is the clobber key from the edit form.
type SpliceInput struct {
	IDInput
	Title string
	// Lead is the text above the heading. Empty means the heading stays first.
	Lead string
	Body string
	Base string
}

// ClobberError is a stale edit form: the file changed, so nothing is written.
type ClobberError struct {
	ID string
}

func (e *ClobberError) Error() string {
	return fmt.Sprintf("%s changed since this form was rendered — reload and save again", e.ID)
}

// Splice replaces the post-fence H1 and body and leaves the fence bytes unchanged.
// It does not self-commit. Durability matches design create.
func Splice(deps Deps, in SpliceInput) (Result, error) {
	titleText, err := bodyedit.NormalizeTitle(in.Title)
	if err != nil {
		return Result{}, &UsageError{Msg: err.Error()}
	}
	if strings.TrimSpace(in.Base) == "" {
		return Result{}, &UsageError{Msg: "splice needs a clobber predicate"}
	}

	release, err := lock(in.Dir)
	if err != nil {
		return Result{}, err
	}
	defer release()
	if err := deps.refuseUnusable(in.Scope, in.Dir); err != nil {
		return Result{}, err
	}
	if err := deps.refuseMidRebase(in.Scope, in.Dir); err != nil {
		return Result{}, err
	}

	f, err := resolveOne(in.IDInput)
	if err != nil {
		return Result{}, err
	}
	if f.ParseErr != nil {
		return Result{}, f.parseError()
	}
	data, key, err := bodyedit.Snapshot(f.Path)
	if err != nil {
		return Result{}, err
	}
	if key != in.Base {
		return Result{}, &ClobberError{ID: f.ID}
	}
	_, body, present := frontmatter.Split(data)
	if !present {
		return Result{}, &ParseError{ID: f.ID, Msg: "no frontmatter fence"}
	}
	fence := data[:len(data)-len(body)]
	if err := writeFile(f.Path, bodyedit.Replace(fence, titleText, in.Lead, in.Body)); err != nil {
		return Result{}, err
	}
	if err := deps.syncOne(in.Scope, []string{f.Path}); err != nil {
		return Result{}, err
	}
	abs, err := absPath(f.Path)
	if err != nil {
		return Result{}, err
	}
	return Result{ID: f.ID, Path: abs, SyncNeeded: deps.syncNeeded(in.Scope, in.Dir)}, nil
}
