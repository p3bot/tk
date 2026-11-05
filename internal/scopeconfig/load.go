package scopeconfig

import (
	"os"
	"path/filepath"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/build"
	"cuelang.org/go/cue/load"
)

// Load evaluates a scope's tk.cue with its package, so sibling .cue files count,
// and returns the validated Schema. Every unusable state returns *ConfigError.
func Load(ctx *cue.Context, dir string) (*Schema, error) {
	entry := filepath.Join(dir, "tk.cue")
	if _, err := os.Stat(entry); err != nil {
		if os.IsNotExist(err) {
			return nil, &ConfigError{Dir: dir, Reason: "tk.cue is absent"}
		}
		return nil, &ConfigError{Dir: dir, Reason: "cannot read tk.cue: " + err.Error()}
	}

	inst := loadInstance(dir)
	if inst.Err != nil {
		return nil, &ConfigError{Dir: dir, Reason: cueReason(inst.Err)}
	}
	return Evaluate(dir, ctx.BuildInstance(inst))
}

// loadInstance prefers the directory package (multi-file unify) and falls back to
// the single entry file for package-less minimal configs.
func loadInstance(dir string) *build.Instance {
	cfg := &load.Config{Dir: dir}
	if insts := load.Instances([]string{"."}, cfg); len(insts) > 0 && insts[0].Err == nil {
		return insts[0]
	}
	insts := load.Instances([]string{"./tk.cue"}, cfg)
	if len(insts) == 0 {
		// Loader always returns ≥1 instance for a named file; zero means a CUE-internal break.
		return &build.Instance{Dir: dir}
	}
	return insts[0]
}
