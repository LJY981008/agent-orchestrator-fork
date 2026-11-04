package codexappserver

// LADO fork: the compact limit of a project in LADO mode (issue #42). Codex
// Chat runs app-server without -c options, so the session manager puts the
// limit on the launch environment under AO's own name
// (domain.LadoCodexCompactLimitEnv). The driver takes it off the
// environment the app-server starts with and sends it in the thread config
// as model_auto_compact_token_limit, which codex-cli 0.146.0's app-server
// schema lists among a thread's config fields.

import (
	"context"
	"maps"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ladoCompactCarrier takes the limit off the launch environments of one
// connect and keeps it for the thread config.
type ladoCompactCarrier struct {
	limit atomic.Int64
}

// take returns env without the limit's variable, a copy when it had it, and
// keeps the limit it named.
func (c *ladoCompactCarrier) take(env map[string]string) map[string]string {
	value, ok := env[domain.LadoCodexCompactLimitEnv]
	if !ok {
		return env
	}
	out := maps.Clone(env)
	delete(out, domain.LadoCodexCompactLimitEnv)
	if n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && n > 0 {
		c.limit.Store(n)
	}
	return out
}

// wrap returns prepare with the limit taken off what it returns.
func (c *ladoCompactCarrier) wrap(prepare func(context.Context) (map[string]string, error)) func(context.Context) (map[string]string, error) {
	if prepare == nil {
		return nil
	}
	return func(ctx context.Context) (map[string]string, error) {
		env, err := prepare(ctx)
		if err != nil {
			return nil, err
		}
		return c.take(env), nil
	}
}

// withLadoCompactLimit adds the limit to a thread config, which it returns.
// A limit of zero leaves the config alone.
func withLadoCompactLimit(config map[string]any, limit int64) map[string]any {
	if limit <= 0 {
		return config
	}
	if config == nil {
		config = map[string]any{}
	}
	config[domain.LadoCodexCompactLimitKey] = limit
	return config
}
