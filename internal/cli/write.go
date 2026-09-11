package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/depgate"
	"github.com/p3bot/tk/internal/token"
	"github.com/p3bot/tk/internal/writeengine"
)

// emitWriteResult maps a writeengine outcome onto stdout path, stderr tokens, and exit classes.
func emitWriteResult(c *cobra.Command, res writeengine.Result, err error) error {
	for _, w := range res.Warnings {
		stderrln(c, w)
	}
	if res.ApplyLens {
		stderrln(c, lensEcho(res.Lens))
	}
	for _, line := range res.SelectionTokens {
		stderrln(c, line)
	}

	tickets := res.Tickets()
	hasPath := false
	for _, m := range tickets {
		if m.Path != "" {
			stdoutln(c, m.Path)
			hasPath = true
		}
	}
	// Path on the result means the write landed; print side tokens then too.
	printPath := err == nil || errors.Is(err, writeengine.ErrPushFailed) || hasPath
	for _, line := range res.SyncDisabledLines() {
		stderrln(c, token.Line(token.SyncDisabled, line))
	}
	for _, line := range res.SyncNeededLines() {
		stderrln(c, token.Line(token.SyncNeeded, line))
	}
	for _, line := range res.EdgeVerify {
		stderrln(c, token.Line(token.EdgeVerify, line))
	}
	if printPath {
		for _, m := range tickets {
			if len(m.DependsOpen) > 0 {
				stderrln(c, token.FormatDependsOpen(m.ID, m.NewStatus, m.DependsOpen))
			}
			if len(m.RequiredMissing) > 0 {
				stderrln(c, token.FormatRequiredMissing(m.ID, m.RequiredMissing))
			}
		}
	}
	if printPath && res.ArchiveNote != "" {
		stderrln(c, res.ArchiveNote)
	}
	if printPath && res.ScaffoldCue != "" {
		stderrln(c, res.ScaffoldCue)
	}
	if printPath {
		for _, tag := range res.TagNew {
			stderrln(c, token.FormatTagNew(tag))
		}
	}
	return mapWriteErr(err)
}

func mapWriteErr(err error) error {
	if err == nil {
		return nil
	}
	var use *writeengine.UsageError
	if errors.As(err, &use) {
		return usageErrorf("%s", use.Msg)
	}
	var unk *writeengine.UnknownStatusError
	if errors.As(err, &unk) {
		return usageErrorf("%q is not a known status for scope %q", unk.Status, unk.Scope)
	}
	var empty *depgate.EmptyQueueError
	if errors.As(err, &empty) {
		return &ExitError{Code: exitFailure, Plain: true, Err: empty}
	}
	var nl *writeengine.NoLongerTodoError
	if errors.As(err, &nl) {
		return &ExitError{Code: exitFailure, Plain: true, Err: nl}
	}
	if errors.Is(err, writeengine.ErrRefreshFailed) {
		return &ExitError{Code: exitFailure, Plain: true, Err: writeengine.ErrRefreshFailed}
	}
	if errors.Is(err, writeengine.ErrPushFailed) {
		return &ExitError{Code: exitFailure, Plain: true, Err: writeengine.ErrPushFailed}
	}
	return err
}
