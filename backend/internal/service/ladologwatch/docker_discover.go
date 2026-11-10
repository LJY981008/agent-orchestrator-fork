package ladologwatch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// dockerReplicaSuffix is a replica suffix on a container name: "-1", "_2".
var dockerReplicaSuffix = regexp.MustCompile(`[-_]\d+$`)

// dockerListed is one entry of GET /containers/json.
type dockerListed struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	Labels map[string]string `json:"Labels"`
	State  string            `json:"State"`
}

// Discover lists the running containers the source's targets select. Each
// target is one GET /containers/json with its filters as JSON (no "all", so
// only running containers); the package then checks each answer again by
// itself, so that an Engine reading a filter more loosely (a name filter is
// an unanchored regular expression) does not widen a target. A container two
// targets select is one instance, with the first target in the settings'
// order. Each instance's TTY, start time and image come from GET
// /containers/{id}/json; a container gone in between is left out.
func (s *DockerSource) Discover(ctx context.Context) ([]Instance, error) {
	type picked struct {
		listed dockerListed
		target DockerTarget
	}
	var order []picked
	seen := map[string]bool{}
	for _, target := range s.cfg.Targets {
		filters, err := dockerFilters(target)
		if err != nil {
			return nil, err
		}
		var listed []dockerListed
		if err := s.dockerGetJSON(ctx, "/containers/json", url.Values{"filters": {filters}}, &listed); err != nil {
			return nil, err
		}
		for _, c := range listed {
			if !dockerIDPattern.MatchString(c.ID) || seen[c.ID] || !dockerMatches(target, c) {
				continue
			}
			seen[c.ID] = true
			order = append(order, picked{listed: c, target: target})
		}
	}
	instances := make([]Instance, 0, len(order))
	for _, p := range order {
		c, err := s.dockerInspect(ctx, p.listed.ID)
		if dockerNotFound(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if c.Name == "" {
			c.Name = dockerListedName(p.listed)
		}
		if c.Labels == nil {
			c.Labels = p.listed.Labels
		}
		image := c.Image
		if image == "" {
			image = p.listed.Image
		}
		instances = append(instances, Instance{
			ID:        p.listed.ID,
			Name:      c.Name,
			Image:     image,
			Service:   dockerServiceName(p.target, c.Labels, c.Name),
			StartedAt: c.StartedAt,
			Tty:       c.Tty,
			TargetID:  p.target.ID,
		})
	}
	return instances, nil
}

// dockerFilters is a target's filters JSON for GET /containers/json.
func dockerFilters(t DockerTarget) (string, error) {
	filters := map[string][]string{}
	switch t.Kind {
	case dockerTargetComposeProject:
		filters["label"] = []string{dockerLabelComposeProject + "=" + t.Value}
	case dockerTargetComposeService:
		filters["label"] = []string{dockerLabelComposeService + "=" + t.Value}
		if t.ComposeProject != "" {
			filters["label"] = append(filters["label"], dockerLabelComposeProject+"="+t.ComposeProject)
		}
	case dockerTargetName:
		// The Engine matches a name filter as an unanchored regular
		// expression against "/<name>"; anchor it and quote the value.
		filters["name"] = []string{"^/" + regexp.QuoteMeta(t.Value) + "$"}
	case dockerTargetLabel:
		filters["label"] = []string{t.Value}
	default:
		return "", fmt.Errorf("docker target kind %q is not known", t.Kind)
	}
	if t.Value == "" {
		return "", fmt.Errorf("docker target %q has no value", t.ID)
	}
	data, err := json.Marshal(filters)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// dockerMatches checks a listed container against its target by itself.
func dockerMatches(t DockerTarget, c dockerListed) bool {
	switch t.Kind {
	case dockerTargetComposeProject:
		return c.Labels[dockerLabelComposeProject] == t.Value
	case dockerTargetComposeService:
		if c.Labels[dockerLabelComposeService] != t.Value {
			return false
		}
		return t.ComposeProject == "" || c.Labels[dockerLabelComposeProject] == t.ComposeProject
	case dockerTargetName:
		for _, name := range c.Names {
			if strings.TrimPrefix(name, "/") == t.Value {
				return true
			}
		}
		return false
	case dockerTargetLabel:
		key, value, hasValue := strings.Cut(t.Value, "=")
		got, ok := c.Labels[key]
		return ok && (!hasValue || got == value)
	}
	return false
}

// dockerListedName is a listed container's own name: the first name without
// a further "/" (the others are link aliases).
func dockerListedName(c dockerListed) string {
	for _, name := range c.Names {
		name = strings.TrimPrefix(name, "/")
		if name != "" && !strings.Contains(name, "/") {
			return name
		}
	}
	if len(c.Names) > 0 {
		return strings.TrimPrefix(c.Names[0], "/")
	}
	return ""
}

// dockerServiceName is the service name an instance's fingerprints are made
// with: the target's override first, then "<project>/<service>" from the
// compose labels (the service alone without a project label), then the
// container name without a replica suffix. Containers that compose made with
// the same -p project on several servers so share one service name.
func dockerServiceName(t DockerTarget, labels map[string]string, name string) string {
	if t.Service != "" {
		return t.Service
	}
	if service := labels[dockerLabelComposeService]; service != "" {
		if project := labels[dockerLabelComposeProject]; project != "" {
			return project + "/" + service
		}
		return service
	}
	if base := dockerReplicaSuffix.ReplaceAllString(name, ""); base != "" {
		return base
	}
	return name
}
