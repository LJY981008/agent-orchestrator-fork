package codex

// LADO fork: the -c options that carry the setup LADO manages into a Codex
// session (docs/LJY-PRD/HARNESS.md §9).

import "context"

// SetLadoOverrides installs LADO's -c options, which every launch and restore
// command of this adapter gets after AO's own options; an error fails the
// command. The daemon calls it on the adapter that launches sessions before
// anything spawns. nil keeps upstream behavior.
func (p *Plugin) SetLadoOverrides(fn func(ctx context.Context, sessionID string) ([]string, error)) {
	p.ladoOverrides = fn
}

func (p *Plugin) appendLadoOverrides(ctx context.Context, sessionID string, args *[]string) error {
	if p.ladoOverrides == nil {
		return nil
	}
	extra, err := p.ladoOverrides(ctx, sessionID)
	*args = append(*args, extra...)
	return err
}
