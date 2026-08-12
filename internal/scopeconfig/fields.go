package scopeconfig

import (
	"errors"
	"fmt"
	"path/filepath"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/parser"

	"github.com/p3bot/tk/internal/atomicfile"
)

// ErrFieldNotDeclared reports that the named field is absent from fields: in
// tk.cue (including when the fields: key itself is absent). Callers that already
// saw the name in a package-unified schema may rewrite this into sibling guidance.
var ErrFieldNotDeclared = errors.New("field is not declared")

// SetField creates or fully replaces one field declaration under fields: in
// <dir>/tk.cue via CUE AST (no string templating). Unrelated top-level keys,
// sibling fields, and comments are preserved as far as format.Node allows.
//
// Full replace: the written declaration is built only from f — prior required
// and values are not sticky when omitted from f.
func SetField(dir, name string, f Field) error {
	if err := ValidateFieldDecl(name, f); err != nil {
		return err
	}
	p := filepath.Join(dir, "tk.cue")
	file, err := parser.ParseFile(p, nil, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse %s: %w", p, err)
	}

	decl := fieldDeclAST(name, f)
	fieldsLit, fieldsField, err := findFieldsStruct(file)
	if err != nil {
		return fmt.Errorf("%s: %w", p, err)
	}
	if fieldsField == nil {
		file.Decls = append(file.Decls, &ast.Field{
			Label: ast.NewIdent("fields"),
			Value: ast.NewStruct(decl),
		})
	} else {
		replaceOrAppendField(fieldsLit, name, decl)
	}

	data, err := format.Node(file)
	if err != nil {
		return fmt.Errorf("format %s: %w", p, err)
	}
	return atomicfile.Write(p, data, 0o600)
}

// UnsetField removes one field declaration from fields: in <dir>/tk.cue.
// It does not open or rewrite ticket markdown. Unknown name is an error.
// When the last field is removed, the top-level fields key is dropped.
func UnsetField(dir, name string) error {
	if name == "" {
		return fmt.Errorf("field name is empty")
	}
	p := filepath.Join(dir, "tk.cue")
	file, err := parser.ParseFile(p, nil, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse %s: %w", p, err)
	}

	fieldsLit, fieldsField, err := findFieldsStruct(file)
	if err != nil {
		return fmt.Errorf("%s: %w", p, err)
	}
	if fieldsField == nil {
		return fmt.Errorf("field %q is not declared: %w", name, ErrFieldNotDeclared)
	}

	found := false
	out := make([]ast.Decl, 0, len(fieldsLit.Elts))
	for _, e := range fieldsLit.Elts {
		ef, ok := e.(*ast.Field)
		if !ok {
			out = append(out, e)
			continue
		}
		if labelName(ef.Label) == name {
			found = true
			continue
		}
		out = append(out, e)
	}
	if !found {
		return fmt.Errorf("field %q is not declared: %w", name, ErrFieldNotDeclared)
	}

	if countFieldDecls(out) == 0 {
		// Drop the entire fields: key when nothing remains.
		file.Decls = removeTopLevelField(file.Decls, "fields")
	} else {
		fieldsLit.Elts = out
	}

	data, err := format.Node(file)
	if err != nil {
		return fmt.Errorf("format %s: %w", p, err)
	}
	return atomicfile.Write(p, data, 0o600)
}

func fieldDeclAST(name string, f Field) *ast.Field {
	parts := []any{
		ast.NewIdent("type"), ast.NewString(f.Type),
	}
	if f.Required {
		parts = append(parts, ast.NewIdent("required"), ast.NewBool(true))
	}
	if f.Values != nil {
		exprs := make([]ast.Expr, len(f.Values))
		for i, v := range f.Values {
			exprs[i] = ast.NewString(v)
		}
		parts = append(parts, ast.NewIdent("values"), ast.NewList(exprs...))
	}
	return &ast.Field{
		Label: ast.NewIdent(name),
		Value: ast.NewStruct(parts...),
	}
}

func findFieldsStruct(file *ast.File) (*ast.StructLit, *ast.Field, error) {
	for _, d := range file.Decls {
		fld, ok := d.(*ast.Field)
		if !ok {
			continue
		}
		if labelName(fld.Label) != "fields" {
			continue
		}
		if fld.Value == nil {
			return nil, nil, fmt.Errorf("fields has no value")
		}
		st, ok := fld.Value.(*ast.StructLit)
		if !ok {
			return nil, nil, fmt.Errorf("fields is not a struct literal (cannot rewrite via scope field)")
		}
		return st, fld, nil
	}
	return nil, nil, nil
}

func replaceOrAppendField(st *ast.StructLit, name string, decl *ast.Field) {
	for i, e := range st.Elts {
		ef, ok := e.(*ast.Field)
		if !ok {
			continue
		}
		if labelName(ef.Label) == name {
			st.Elts[i] = decl
			return
		}
	}
	st.Elts = append(st.Elts, decl)
}

func countFieldDecls(elts []ast.Decl) int {
	n := 0
	for _, e := range elts {
		if _, ok := e.(*ast.Field); ok {
			n++
		}
	}
	return n
}

func removeTopLevelField(decls []ast.Decl, name string) []ast.Decl {
	out := make([]ast.Decl, 0, len(decls))
	for _, d := range decls {
		fld, ok := d.(*ast.Field)
		if ok && labelName(fld.Label) == name {
			continue
		}
		out = append(out, d)
	}
	return out
}
