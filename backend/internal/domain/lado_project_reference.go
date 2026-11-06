package domain

// LADO fork: a project's skills, agents and instruction files that a LADO
// session's instructions list as the project's experts (#29,
// docs/LJY-PRD/HARNESS.md §9.4). service/ladosetup reads them from the
// project's repository and service/ladotask renders them for the session's
// role and tool, so the type lives here to keep the two apart.

// LadoProjectReference is one file a project's repository tracks that a
// session can use: a skill's SKILL.md, an agent file or an instruction file.
type LadoProjectReference struct {
	// Kind is LadoSetupKindSkill, LadoSetupKindAgent or
	// LadoSetupKindInstructions.
	Kind LadoSetupKind
	// Loader is the tool that reads the file by itself in a worktree:
	// LadoSetupToolClaudeCode or LadoSetupToolCodex.
	Loader LadoSetupTool
	// Path is relative to the repository root, with slashes.
	Path string
	// Name is the skill's or agent's name on one line; for an instruction
	// file it is Path.
	Name string
	// Description is the skill's or agent's description on one line, with
	// its when_to_use; "" when the file could not be read.
	Description string
}
