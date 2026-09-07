package scopeconfig

import (
	"fmt"
	"path/filepath"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/parser"

	"github.com/p3bot/tk/internal/atomicfile"
)

// RewriteAutoCommit rewrites only the top-level autoCommit field of <dir>/tk.cue
// via CUE AST (no string templating), preserving other fields, comments, and
// formatting. The field must already exist; this does not insert it.
func RewriteAutoCommit(dir string, autoCommit bool) error {
	p := filepath.Join(dir, "tk.cue")
	file, err := parser.ParseFile(p, nil, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse %s: %w", p, err)
	}

	found := false
	for _, decl := range file.Decls {
		field, ok := decl.(*ast.Field)
		if !ok {
			continue
		}
		if labelName(field.Label) != "autoCommit" {
			continue
		}
		newValue := ast.NewBool(autoCommit)
		ast.SetComments(newValue, ast.Comments(field.Value))
		field.Value = newValue
		found = true
		break
	}
	if !found {
		return fmt.Errorf("%s has no top-level autoCommit field to rewrite", p)
	}

	data, err := format.Node(file)
	if err != nil {
		return fmt.Errorf("format %s: %w", p, err)
	}
	return atomicfile.Write(p, data, 0o600)
}
