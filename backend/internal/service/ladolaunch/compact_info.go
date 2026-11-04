package ladolaunch

// The settings that turn Claude Code's auto-compact off, so the compact
// window LADO gives a session does nothing. Claude Code 2.1.317 reads them as
// "the autoCompactEnabled setting + DISABLE_AUTO_COMPACT / DISABLE_COMPACT
// env": autoCompactEnabled in a settings file or the global config, and the
// two variables in the environment or a settings file's env block.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Where a compact warning was found.
const (
	SourceProjectSettings      = "project_settings"
	SourceProjectLocalSettings = "project_local_settings"
	SourceUserSettings         = "user_settings"
	SourceUserConfig           = "user_config"
	SourceLadoSettings         = "lado_settings"
	SourceProjectEnv           = "project_env"
	SourceLaunchEnv            = "launch_env"
	SourceDaemonEnv            = "daemon_env"
)

// The settings that turn auto-compact off.
const (
	SettingAutoCompactEnabled = "autoCompactEnabled"
	SettingDisableAutoCompact = "DISABLE_AUTO_COMPACT"
	SettingDisableCompact     = "DISABLE_COMPACT"
)

// disableEnvKeys are the variables that turn auto-compact off.
var disableEnvKeys = []string{SettingDisableAutoCompact, SettingDisableCompact}

// maxSettingsBytes caps a settings file LADO reads; Claude's global config
// can grow to a few megabytes.
const maxSettingsBytes = 32 << 20

// CompactWarning is one setting that turns Claude Code's auto-compact off.
type CompactWarning struct {
	// Source is where it was found (Source*).
	Source string `json:"source"`
	// Setting is the setting or variable (Setting*).
	Setting string `json:"setting"`
	// Path is the file it was found in, empty for an environment.
	Path string `json:"path,omitempty"`
}

// CompactInfo is what LADO does about a project's compaction.
type CompactInfo struct {
	// Applies is whether the project's sessions get the compact window: the
	// project is in LADO mode.
	Applies bool
	// Window is the project's compact window setting.
	Window int
	// Warnings are the settings that turn auto-compact off, so the window
	// does nothing for Claude Code.
	Warnings []CompactWarning
}

// CompactInfo returns the project's compact window and the settings in its
// repository, the computer's Claude setup, LADO's managed Claude settings and
// the project's environment that turn auto-compact off.
func (s *Service) CompactInfo(ctx context.Context, project domain.ProjectID) (CompactInfo, error) {
	window, ok, err := s.CompactWindow(ctx, project)
	if err != nil {
		return CompactInfo{}, err
	}
	info := CompactInfo{Applies: ok, Window: window}
	if !ok {
		info.Window = domain.LadoDefaultCompactWindow
		if s != nil && s.opts.Store != nil && project != "" {
			watch, err := s.opts.Store.LadoEffectiveWatchSettings(ctx, project)
			if err != nil {
				return CompactInfo{}, err
			}
			info.Window = watch.CompactWindow
		}
	}
	info.Warnings = s.compactWarnings(ctx, compactInput{project: project})
	if info.Warnings == nil {
		info.Warnings = []CompactWarning{}
	}
	return info, nil
}

// compactInput says what compactWarnings reads. With env nil it reads the
// project's environment; with env set, a launch's.
type compactInput struct {
	project   domain.ProjectID
	workspace string
	env       map[string]string
}

func (s *Service) compactWarnings(ctx context.Context, in compactInput) []CompactWarning {
	if s == nil {
		return nil
	}
	var out []CompactWarning
	add := func(w CompactWarning) {
		for _, have := range out {
			if have == w {
				return
			}
		}
		out = append(out, w)
	}

	var project domain.ProjectRecord
	if s.opts.Projects != nil && in.project != "" {
		if rec, found, err := s.opts.Projects.GetProject(ctx, string(in.project)); err != nil {
			s.opts.Log.Warn("LADO could not read a project to look for auto-compact settings", "project", in.project, "err", err)
		} else if found {
			project = rec
		}
	}

	// The repository's own settings, in the session's worktree and the
	// project's checkout (settings.local.json stays in the checkout).
	for _, dir := range uniqueDirs(in.workspace, project.Path) {
		claudeDir := filepath.Join(dir, ".claude")
		for _, w := range settingsWarnings(filepath.Join(claudeDir, "settings.json"), SourceProjectSettings) {
			add(w)
		}
		for _, w := range settingsWarnings(filepath.Join(claudeDir, "settings.local.json"), SourceProjectLocalSettings) {
			add(w)
		}
	}

	// The computer's Claude setup: the config folder the session runs on.
	configDir, globalConfig := s.claudeConfigPaths(in.env)
	if configDir != "" {
		for _, w := range settingsWarnings(filepath.Join(configDir, "settings.json"), SourceUserSettings) {
			add(w)
		}
	}
	if globalConfig != "" {
		for _, w := range settingsWarnings(globalConfig, SourceUserConfig) {
			add(w)
		}
	}

	// LADO's managed Claude settings, which a LADO config home lays over the
	// computer's.
	if s.opts.DataDir != "" {
		for _, w := range settingsWarnings(filepath.Join(s.opts.DataDir, "lado", "harness", "claude", "settings.json"), SourceLadoSettings) {
			add(w)
		}
	}

	if in.env != nil {
		for _, w := range envWarnings(in.env, SourceLaunchEnv) {
			add(w)
		}
	} else {
		for _, w := range envWarnings(project.Config.Env, SourceProjectEnv) {
			add(w)
		}
	}
	for _, key := range disableEnvKeys {
		if truthy(s.opts.Getenv(key)) {
			add(CompactWarning{Source: SourceDaemonEnv, Setting: key})
		}
	}
	return out
}

// claudeConfigPaths returns the Claude config folder a session runs on and
// its global config file: CLAUDE_CONFIG_DIR of the launch or the daemon, or
// ~/.claude and ~/.claude.json.
func (s *Service) claudeConfigPaths(env map[string]string) (configDir, globalConfig string) {
	dir := strings.TrimSpace(env["CLAUDE_CONFIG_DIR"])
	if dir == "" {
		dir = strings.TrimSpace(s.opts.Getenv("CLAUDE_CONFIG_DIR"))
	}
	if dir != "" {
		return dir, filepath.Join(dir, ".claude.json")
	}
	home := s.opts.Home
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return "", ""
		}
	}
	return filepath.Join(home, ".claude"), filepath.Join(home, ".claude.json")
}

// settingsWarnings reads a Claude settings or config file: autoCompactEnabled
// false at its top, and a truthy DISABLE_AUTO_COMPACT or DISABLE_COMPACT in
// its env block. A missing or unreadable file has none.
func settingsWarnings(path, source string) []CompactWarning {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSettingsBytes {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var file struct {
		AutoCompactEnabled *bool          `json:"autoCompactEnabled"`
		Env                map[string]any `json:"env"`
	}
	if json.Unmarshal(data, &file) != nil {
		return nil
	}
	var out []CompactWarning
	if file.AutoCompactEnabled != nil && !*file.AutoCompactEnabled {
		out = append(out, CompactWarning{Source: source, Setting: SettingAutoCompactEnabled, Path: path})
	}
	for _, key := range disableEnvKeys {
		if v, ok := file.Env[key]; ok && truthyValue(v) {
			out = append(out, CompactWarning{Source: source, Setting: key, Path: path})
		}
	}
	return out
}

// envWarnings returns the variables of env that turn auto-compact off.
func envWarnings(env map[string]string, source string) []CompactWarning {
	var out []CompactWarning
	for _, key := range disableEnvKeys {
		if truthy(env[key]) {
			out = append(out, CompactWarning{Source: source, Setting: key})
		}
	}
	return out
}

// truthy reads a variable as Claude Code does: 1, true, yes or on.
func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// truthyValue reads a settings env value, a string, a bool or a number.
func truthyValue(v any) bool {
	switch v := v.(type) {
	case string:
		return truthy(v)
	case bool:
		return v
	case float64:
		return v == 1
	}
	return false
}

// uniqueDirs returns the non-empty folders, each once.
func uniqueDirs(dirs ...string) []string {
	var out []string
	seen := map[string]bool{}
	for _, dir := range dirs {
		if dir = strings.TrimSpace(dir); dir == "" {
			continue
		}
		clean := filepath.Clean(dir)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		out = append(out, clean)
	}
	return out
}
