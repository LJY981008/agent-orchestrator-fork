package ladologwatch

import (
	"context"
	"time"
)

// LogSource is one log source of a project: a place the daemon finds the
// instances (containers) to watch and follows their logs. The Follower, the
// Detector and the incident handler do not know the kind of source; the
// Docker Engine API over tcp (DockerSource) is the one kind for now, and a
// second kind (SSH, say) can be added behind the same interface (ADR 0012).
type LogSource interface {
	// Discover lists the running instances the source's targets select. An
	// instance that two targets select comes once, with the first target in
	// the settings' order.
	Discover(ctx context.Context) ([]Instance, error)
	// Follow reads the logs of one instance from a point on and calls onLine
	// for each line until ctx ends or the stream ends. stream is "stdout" or
	// "stderr"; ts is the line's timestamp on the source's clock.
	Follow(ctx context.Context, inst Instance, from FollowFrom, onLine func(stream string, ts time.Time, text string)) error
}

// Instance is one container a source watches.
type Instance struct {
	// ID is the container id (lowercase hex).
	ID string
	// Name is the container name without the leading "/".
	Name string
	// Image is the image reference the container runs.
	Image string
	// Service is the service name the instance's fingerprints are made with:
	// the target's service override, else "<compose project>/<compose
	// service>" from the compose labels, else the container name without a
	// replica suffix.
	Service string
	// StartedAt is when the container started, on the source's clock.
	StartedAt time.Time
	// Tty is whether the container has a TTY: its log stream is then raw,
	// not multiplexed.
	Tty bool
	// TargetID is the target that selected the instance (the first one, when
	// several did).
	TargetID string
}

// FollowFrom is where a Follow starts: at Since, on the source's clock (the
// caller still skips lines at or before its cursor), or, with TailZero, at
// the end of the log so that only new lines come.
type FollowFrom struct {
	Since    time.Time
	TailZero bool
}
