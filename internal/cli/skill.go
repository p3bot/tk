package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/p3bot/agentdex"
	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/skill"
)

func newSkillCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "skill",
		Aliases: []string{"skills"},
		Short:   "Print the agent skill contract or manage installs",
		Long: "Print the locked agent skill contract to stdout as agent-facing workflow\n" +
			"markdown. No ambient scope is required. Alias: skills.\n\n" +
			"Subcommands install, list, and uninstall place or remove the skill under\n" +
			"agent skills directories resolved via agentdex (no hardcoded product paths).",
		Args: noArgs(),
		RunE: func(c *cobra.Command, _ []string) error {
			_, err := fmt.Fprint(c.OutOrStdout(), skill.Text())
			return err
		},
	}
	cmd.AddCommand(
		newSkillInstallCmd(app),
		newSkillListCmd(app),
		newSkillUninstallCmd(app),
	)
	return cmd
}

func newSkillInstallCmd(app *App) *cobra.Command {
	var local bool
	cmd := &cobra.Command{
		Use:   "install [agents...]",
		Short: "Install the skill into agent skills directories",
		Long: "Write the embedded skill contract to agent skills directories\n" +
			"resolved by agentdex. With no agent args, installs to Primary for each\n" +
			"installed agent that has a skills concept. Named agents use Native if\n" +
			"set, else Shared (agents role). Paths de-dupe so a shared root is written\n" +
			"once; written paths are printed in alphabetical order.",
		Args: anyArgs(),
		RunE: func(c *cobra.Command, args []string) error {
			return runSkillInstall(c, app, args, local)
		},
	}
	cmd.Flags().BoolVar(&local, "local", false, "use project-local skills roots only")
	return cmd
}

func newSkillListCmd(app *App) *cobra.Command {
	var local bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List installed skill copies for installed agents",
		Long: "Inventory existing tk/SKILL.md paths under candidates of installed\n" +
			"agents that have a skills concept. No agent positionals. Paths print in\n" +
			"alphabetical order. Empty inventory exits 0 with empty stdout and a stderr note.",
		Args: noArgs(),
		RunE: func(c *cobra.Command, _ []string) error {
			return runSkillList(c, app, local)
		},
	}
	cmd.Flags().BoolVar(&local, "local", false, "use project-local skills roots only")
	return cmd
}

func newSkillUninstallCmd(app *App) *cobra.Command {
	var local bool
	cmd := &cobra.Command{
		Use:   "uninstall [agents...]",
		Short: "Remove installed skill copies",
		Long: "Remove owned pure tk/ skill directories under candidates of the target\n" +
			"agent set. Multi-tenant paths still claimed by other installed agents are\n" +
			"kept (reported, not an error). Foreign files or wrong frontmatter name keep\n" +
			"the dir. Report lines are ordered alphabetically by path.",
		Args: anyArgs(),
		RunE: func(c *cobra.Command, args []string) error {
			return runSkillUninstall(c, app, args, local)
		},
	}
	cmd.Flags().BoolVar(&local, "local", false, "use project-local skills roots only")
	return cmd
}

func skillLocation(local bool) skill.Location {
	if local {
		return skill.LocationLocal
	}
	return skill.LocationGlobal
}

// openSkillIndex builds an agentdex index for skill path ops.
// Local roots need a real working directory; global-only tolerates Getwd failure
// by falling back to "/" so ~ expansion still works.
func openSkillIndex(app *App, local bool) (*agentdex.Index, error) {
	wd, err := os.Getwd()
	if err != nil {
		if local {
			return nil, fmt.Errorf("working directory required for --local: %w", err)
		}
		wd = "/"
	}
	return skill.OpenIndex(wd, app.AgentdexOpts...)
}

func runSkillInstall(c *cobra.Command, app *App, agentIDs []string, local bool) error {
	ctx := c.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	idx, err := openSkillIndex(app, local)
	if err != nil {
		return err
	}
	loc := skillLocation(local)
	named := len(agentIDs) > 0

	var agents []agentdex.Agent
	if named {
		agents, err = skill.ResolveExplicit(ctx, idx, agentIDs)
		if err != nil {
			return skillUsageError(err)
		}
	} else {
		agents, err = skill.DefaultSet(ctx, idx)
		if err != nil {
			return err
		}
		if len(agents) == 0 {
			return skillUsageError(skill.ErrEmptyAgentSet)
		}
	}

	// De-dupe by absolute skills root; write once per path; print sorted.
	seen := make(map[string]struct{})
	var order []string
	for _, a := range agents {
		r := skill.RootsAt(a, loc)
		root := skill.InstallRoot(r, named)
		if root == "" {
			if named {
				return skillUsageError(skill.NoWritablePathError(a.ID))
			}
			continue
		}
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		order = append(order, root)
	}
	if len(order) == 0 {
		// Default set had skills concept but no Primary at this location.
		return skillUsageError(skill.ErrNoWritablePath)
	}
	skill.SortPaths(order)

	for _, root := range order {
		written, err := skill.WriteInstall(root)
		if err != nil {
			return err
		}
		stdoutln(c, written)
	}
	return nil
}

func runSkillList(c *cobra.Command, app *App, local bool) error {
	ctx := c.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	idx, err := openSkillIndex(app, local)
	if err != nil {
		return err
	}
	loc := skillLocation(local)

	agents, err := skill.DefaultSet(ctx, idx)
	if err != nil {
		return err
	}
	// path → agents claiming it
	claimers := make(map[string][]string)
	for _, a := range agents {
		r := skill.RootsAt(a, loc)
		for _, p := range skill.Candidates(r) {
			if !skill.Present(p) {
				continue
			}
			claimers[p] = append(claimers[p], a.ID)
		}
	}
	paths := make([]string, 0, len(claimers))
	for p := range claimers {
		paths = append(paths, p)
	}
	skill.SortPaths(paths)
	if len(paths) == 0 {
		// Keep stdout path-pure for scripts; humans get a note on stderr.
		stderrln(c, "not installed")
		return nil
	}
	for _, p := range paths {
		file := skill.FilePath(p)
		stdoutln(c, file+"\t"+skill.JoinAgents(claimers[p]))
	}
	return nil
}

func runSkillUninstall(c *cobra.Command, app *App, agentIDs []string, local bool) error {
	ctx := c.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	idx, err := openSkillIndex(app, local)
	if err != nil {
		return err
	}
	loc := skillLocation(local)
	named := len(agentIDs) > 0

	var sAgents []agentdex.Agent
	if named {
		sAgents, err = skill.ResolveExplicit(ctx, idx, agentIDs)
		if err != nil {
			return skillUsageError(err)
		}
	} else {
		sAgents, err = skill.DefaultSet(ctx, idx)
		if err != nil {
			return err
		}
		if len(sAgents) == 0 {
			return skillUsageError(skill.ErrEmptyAgentSet)
		}
	}

	// R = installed + skills concept \ S. When S is already the default set,
	// R is empty by definition — skip a second DefaultSet.
	var rAgents []agentdex.Agent
	if named {
		sIDs := make(map[string]struct{}, len(sAgents))
		for _, a := range sAgents {
			sIDs[a.ID] = struct{}{}
		}
		defaultSet, err := skill.DefaultSet(ctx, idx)
		if err != nil {
			return err
		}
		for _, a := range defaultSet {
			if _, inS := sIDs[a.ID]; !inS {
				rAgents = append(rAgents, a)
			}
		}
	}

	// path → claimers in R (blockers)
	rClaim := make(map[string][]string)
	for _, a := range rAgents {
		r := skill.RootsAt(a, loc)
		for _, p := range skill.Candidates(r) {
			rClaim[p] = append(rClaim[p], a.ID)
		}
	}

	// union of candidates(S)
	pathSet := make(map[string]struct{})
	for _, a := range sAgents {
		r := skill.RootsAt(a, loc)
		for _, p := range skill.Candidates(r) {
			pathSet[p] = struct{}{}
		}
	}
	paths := make([]string, 0, len(pathSet))
	for p := range pathSet {
		paths = append(paths, p)
	}
	skill.SortPaths(paths)

	for _, p := range paths {
		dir := skill.DirPath(p)
		if blockers := rClaim[p]; len(blockers) > 0 {
			// Do not remove under a still-shared root; report presence honestly.
			sort.Strings(blockers)
			if !skill.Present(p) {
				stdoutln(c, fmt.Sprintf("absent\t%s\t%s", dir, skill.JoinAgents(blockers)))
			} else {
				stdoutln(c, fmt.Sprintf("kept\t%s\t%s", dir, skill.JoinAgents(blockers)))
			}
			continue
		}
		res, err := skill.RemoveOwned(p)
		if err != nil {
			return err
		}
		switch res {
		case skill.UninstallRemoved:
			stdoutln(c, "removed\t"+dir)
		case skill.UninstallAbsent:
			stdoutln(c, "absent\t"+dir)
		case skill.UninstallKeptExtra, skill.UninstallKeptNotOurs, skill.UninstallKeptUnreadable:
			stdoutln(c, fmt.Sprintf("kept\t%s\t%s", dir, skill.ReasonDetail(res)))
		default:
			stdoutln(c, res.String()+"\t"+dir)
		}
	}
	return nil
}

// skillUsageError maps skill package usage sentinels to exit 2; other errors pass through.
func skillUsageError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, skill.ErrUnknownAgent) ||
		errors.Is(err, skill.ErrNoSkillsConcept) ||
		errors.Is(err, skill.ErrNoWritablePath) ||
		errors.Is(err, skill.ErrEmptyAgentSet) {
		return &ExitError{Code: exitUsage, Err: err}
	}
	return err
}
