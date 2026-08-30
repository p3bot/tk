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

	printPath := err == nil || errors.Is(err, writeengine.ErrPushFailed)
	if printPath && res.Path != "" {
		stdoutln(c, res.Path)
	}
	if res.SyncDisabled != "" {
		stderrln(c, token.Line(token.SyncDisabled, res.SyncDisabled))
	}
	if res.SyncNeeded != "" {
		stderrln(c, token.Line(token.SyncNeeded, res.SyncNeeded))
	}
	if printPath && len(res.DependsOpen) > 0 {
		stderrln(c, token.FormatDependsOpen(res.ID, res.NewStatus, res.DependsOpen))
	}
	if printPath && len(res.RequiredMissing) > 0 {
		stderrln(c, token.FormatRequiredMissing(res.ID, res.RequiredMissing))
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
