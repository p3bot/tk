package writeengine

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/token"
)

// ParseSkip is one unparseable ticket left untouched during a custom-key strip.
type ParseSkip struct {
	ID  string
	Msg string
}

// TokenLine is the closed parse_error: stderr line for a skipped ticket.
func (s ParseSkip) TokenLine() string {
	return token.Line(token.ParseError, fmt.Sprintf("%s: %s — skipped", s.ID, s.Msg))
}

// StripCustomKey drops name from Custom on every allowlisted ticket under dir
// (root and archive/). Unparseable tickets are skipped. Files that do not carry
// the key are not rewritten. Caller holds the scope flock and owns commit and
// index write-through.
func StripCustomKey(dir, name string) (rewritten []string, skips []ParseSkip, err error) {
	paths, err := scopefile.ListTickets(dir)
	if err != nil {
		return nil, nil, err
	}
	for _, path := range paths {
		changed, skip, err := stripOne(path, name)
		if err != nil {
			return rewritten, skips, err
		}
		if skip != nil {
			skips = append(skips, *skip)
			continue
		}
		if changed {
			rewritten = append(rewritten, path)
		}
	}
	return rewritten, skips, nil
}

func stripOne(path, name string) (changed bool, skip *ParseSkip, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil, fmt.Errorf("read %s: %w", path, err)
	}
	interior, body, present := frontmatter.Split(data)
	id := skipID(path)
	if !present {
		return false, &ParseSkip{ID: id, Msg: "no frontmatter fence"}, nil
	}
	m, err := frontmatter.Parse(interior)
	if err != nil {
		return false, &ParseSkip{ID: id, Msg: err.Error()}, nil
	}
	if !m.RemoveCustom(name) {
		return false, nil, nil
	}
	if err := WriteTicketFile(path, m, body); err != nil {
		return false, nil, err
	}
	return true, nil, nil
}

func skipID(path string) string {
	if id, ok := scopefile.TicketIDFromBase(filepath.Base(path)); ok {
		return id
	}
	return filepath.Base(path)
}
