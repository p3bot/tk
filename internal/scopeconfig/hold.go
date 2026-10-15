package scopeconfig

import (
	"fmt"
	"path/filepath"
	"slices"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/parser"

	"github.com/p3bot/tk/internal/atomicfile"
)

// RewriteHold inserts or rewrites the top-level hold field of <dir>/tk.cue
// via CUE AST. Other fields, comments, and formatting are preserved. A missing
// field is inserted after autoCommit when that field exists, otherwise appended.
func RewriteHold(dir string, hold bool) error {
	p := filepath.Join(dir, "tk.cue")
	file, err := parser.ParseFile(p, nil, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse %s: %w", p, err)
	}

	newValue := ast.NewBool(hold)
	insertAt := -1
	for i, decl := range file.Decls {
		field, ok := decl.(*ast.Field)
		if !ok {
			continue
		}
		switch labelName(field.Label) {
		case "hold":
			ast.SetComments(newValue, ast.Comments(field.Value))
			field.Value = newValue
			return formatHold(p, file)
		case "autoCommit":
			insertAt = i + 1
		}
	}

	added := &ast.Field{Label: ast.NewIdent("hold"), Value: newValue}
	if insertAt >= 0 && insertAt <= len(file.Decls) {
		file.Decls = slices.Insert(file.Decls, insertAt, ast.Decl(added))
	} else {
		file.Decls = append(file.Decls, added)
	}
	return formatHold(p, file)
}

func formatHold(p string, file *ast.File) error {
	data, err := format.Node(file)
	if err != nil {
		return fmt.Errorf("format %s: %w", p, err)
	}
	return atomicfile.Write(p, data, 0o600)
}
