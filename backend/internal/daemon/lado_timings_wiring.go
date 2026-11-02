package daemon

// LADO fork: the timings routes (issue #34) find their service by a type
// assertion on the controller's task service, which would turn a signature
// mismatch into a 501 at run time. This assertion turns it into a build
// failure instead. It lives here because ladotask cannot import controllers.

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
)

var _ controllers.LadoTimingsService = (*ladotask.Service)(nil)
