package codex

// LADO fork: the -c options that carry the setup LADO manages into a Codex
// session (docs/LJY-PRD/HARNESS.md §9), and the compact limit of a project in
// LADO mode (issue #42).

import "context"

// SetLadoOverrides installs LADO's -c options, which every launch and restore
// command of this adapter gets after AO's own options; an error fails the
// command. The daemon calls it on the adapter that launches sessions before
// anything spawns. nil keeps upstream behavior.
func (p *Plugin) SetLadoOverrides(fn func(ctx context.Context, sessionID string) ([]string, error)) {
	p.ladoOverrides = fn
}

// SetLadoCompactOverrides installs the source of the -c option that sets
// model_auto_compact_token_limit (domain.LadoCodexCompactLimitKey) for a
// session, which every launch and restore command gets after LADO's setup
// options. An error leaves the option out: the session starts with Codex's
// own limit rather than not at all. nil keeps upstream behavior.
func (p *Plugin) SetLadoCompactOverrides(fn func(ctx context.Context, sessionID string) ([]string, error)) {
	p.ladoCompact = fn
}

func (p *Plugin) appendLadoOverrides(ctx context.Context, sessionID string, args *[]string) error {
	if p.ladoOverrides != nil {
		extra, err := p.ladoOverrides(ctx, sessionID)
		*args = append(*args, extra...)
		if err != nil {
			return err
		}
	}
	if p.ladoCompact != nil {
		if extra, err := p.ladoCompact(ctx, sessionID); err == nil {
			*args = append(*args, extra...)
		}
	}
	return nil
}
