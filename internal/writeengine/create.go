package writeengine

import (
	"crypto/rand"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/order"
	"github.com/p3bot/tk/internal/slug"
	"github.com/p3bot/tk/internal/status"
)

// CreateInput is one tk create. Identity (ambient / --scope) stays at the edge.
type CreateInput struct {
	Scope  string
	Dir    string
	Title  string
	Status string
	Tags   []string
	Now    time.Time
	Rand   io.Reader
}

// Create mints an unused short-id, scaffolds fence + H1, and never self-commits.
func Create(deps Deps, in CreateInput) (Result, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return Result{}, &UsageError{Msg: "create needs a non-empty title"}
	}
	tags, err := NormalizeCreateTags(in.Tags)
	if err != nil {
		return Result{}, err
	}

	sess, err := Begin(deps, in.Scope, in.Dir)
	if err != nil {
		return Result{}, err
	}
	defer sess.Release()

	custom := sess.Schema.CustomStatuses()
	newStatus := status.Draft
	if in.Status != "" {
		newStatus = in.Status
	}
	if !status.IsKnown(newStatus, custom) {
		return Result{}, &UnknownStatusError{Status: newStatus, Scope: in.Scope}
	}
	if err := sess.CheckMidRebase(); err != nil {
		return Result{}, err
	}

	out := Result{Warnings: sess.Warnings()}

	rows, err := deps.DB.ScopeTickets(in.Scope)
	if err != nil {
		return out, err
	}
	preWriteTags, err := deps.DB.ScopeTagMembership(in.Scope)
	if err != nil {
		return out, err
	}

	src := in.Rand
	if src == nil {
		src = rand.Reader
	}
	shortID, err := mintUnusedID(rows, src)
	if err != nil {
		return out, err
	}
	fullID := in.Scope + "-" + shortID

	orderKey, err := order.KeyBetween(MaxValidOrder(rows), "")
	if err != nil {
		return out, fmt.Errorf("compute append order for %s: %w", fullID, err)
	}

	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	model := &frontmatter.Model{
		ID:      fullID,
		Status:  newStatus,
		Order:   orderKey,
		Tags:    tags,
		Created: now.Format(time.RFC3339),
	}
	interior, err := frontmatter.Serialize(model)
	if err != nil {
		return out, err
	}
	file := frontmatter.Compose(interior, []byte("# "+title+"\n"))

	terminal := status.IsTerminal(newStatus, custom)
	base := fullID + "-" + slug.Slugify(title) + ".md"
	target, err := TerminalLocation(in.Dir, base, terminal)
	if err != nil {
		return out, err
	}
	if err := AtomicWrite(target, file); err != nil {
		return out, err
	}
	if err := deps.Rec.SyncPaths(in.Scope, WrittenPaths(target, "")); err != nil {
		return out, err
	}

	out.ID = fullID
	out.NewStatus = newStatus
	out.ScaffoldCue = fullID + " scaffolded with frontmatter"
	if terminal {
		out.ArchiveNote = fmt.Sprintf("note: %s scaffolded under archive/ — a terminal create is not git-durable until tk sync (auto-commit) or a host commit", fullID)
	}
	if sess.AutoCommit && sess.HasRoot {
		out.SyncNeeded = SyncNeededReason(ctxOf(deps), deps.StateDir, in.Dir, sess.Root)
	}
	out.TagNew = newTagValues(tags, preWriteTags)

	abs, err := absPath(target)
	if err != nil {
		return out, err
	}
	out.Path = abs
	return out, nil
}

// NormalizeCreateTags rejects empty tag values and dedupes preserving first-seen order.
func NormalizeCreateTags(tagArgs []string) ([]string, error) {
	if len(tagArgs) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(tagArgs))
	out := make([]string, 0, len(tagArgs))
	for _, tag := range tagArgs {
		if tag == "" {
			return nil, &UsageError{Msg: "create tag must be non-empty"}
		}
		if _, dup := seen[tag]; dup {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out, nil
}

func mintUnusedID(rows []*index.Ticket, r io.Reader) (string, error) {
	taken := make(map[string]struct{}, len(rows))
	for _, p := range rows {
		if p.ShortID != "" {
			taken[p.ShortID] = struct{}{}
		}
	}
	for {
		s, err := id.Mint(r)
		if err != nil {
			return "", fmt.Errorf("mint id: %w", err)
		}
		if _, used := taken[s]; !used {
			return s, nil
		}
	}
}

func newTagValues(tags []string, inUse map[string]struct{}) []string {
	var out []string
	for _, tag := range tags {
		if tag == "" {
			continue
		}
		if _, ok := inUse[tag]; ok {
			continue
		}
		out = append(out, tag)
	}
	return out
}
