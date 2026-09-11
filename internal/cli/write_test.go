package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/writeengine"
)

func TestEmitWriteResultPrintsLandedPathOnLaterError(t *testing.T) {
	cmd := &cobra.Command{}
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)

	got := emitWriteResult(cmd, writeengine.Result{
		Path: "/tmp/bar-ab2c-work.md",
		ID:   "bar-ab2c",
	}, errors.New("self-commit bar: boom"))
	if got == nil {
		t.Fatal("want the later error to remain")
	}
	if strings.TrimSpace(out.String()) != "/tmp/bar-ab2c-work.md" {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestEmitWriteResultSilentWhenWriteDidNotLand(t *testing.T) {
	cmd := &cobra.Command{}
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)

	got := emitWriteResult(cmd, writeengine.Result{}, errors.New("unknown ticket id \"foo-ab2c\""))
	if got == nil {
		t.Fatal("want the error to remain")
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("stdout must be empty, got %q", out.String())
	}
}
