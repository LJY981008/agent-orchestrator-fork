package ladologwatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The Docker Engine API client is read-only by construction (ADR 0012). An
// Engine on tcp:2375 is an unauthenticated root API for the whole server, so
// one bug here could write to a production server. Three things keep it to
// reads:
//
//   - dockerGet is the one function in the package that makes a request. It
//     always makes a GET without a body.
//   - dockerAllowed is the allow list: GET /version, /containers/json,
//     /containers/{id}/json and /containers/{id}/logs, each with its own query
//     keys, to the configured host only. dockerGet checks it, and the
//     RoundTripper (dockerReadOnlyTransport) checks it again, so a request
//     outside the list fails before it reaches the network.
//   - Redirects are refused and the environment's proxy is ignored, so a
//     request goes to the configured host and nowhere else.
//
// Tests check the allow list, the requests a fake Engine receives, and the
// package's source (no other request constructor, no other method).

// Target kinds a DockerTarget selects containers by. They have the values of
// domain.LadoLogTargetKind.
const (
	dockerTargetComposeProject = "compose_project"
	dockerTargetComposeService = "compose_service"
	dockerTargetName           = "name"
	dockerTargetLabel          = "label"
)

// Compose labels docker compose puts on the containers it makes.
const (
	dockerLabelComposeProject = "com.docker.compose.project"
	dockerLabelComposeService = "com.docker.compose.service"
)

const (
	// dockerDialTimeout bounds connecting to an Engine.
	dockerDialTimeout = 15 * time.Second
	// dockerHeaderTimeout bounds waiting for an Engine's response headers on
	// a request that is not a log stream.
	dockerHeaderTimeout = 15 * time.Second
	// dockerRequestTimeout bounds a whole request that is not a log stream.
	dockerRequestTimeout = 30 * time.Second
	// dockerMaxJSON caps a JSON answer (a container list on a big host).
	dockerMaxJSON = 8 << 20
	// dockerMaxErrorBody caps the error body read from a failed request.
	dockerMaxErrorBody = 4 << 10
)

// DockerConfig is one Docker Engine API source: an endpoint tcp://host:port,
// TLS with the LADO copies of the CA and the client certificate, and the
// targets that select its containers.
type DockerConfig struct {
	// Endpoint is tcp://host:port, as DOCKER_HOST.
	Endpoint string
	// TLS turns on https with CAFile as the only root; CertFile and KeyFile
	// are the client certificate, both or neither.
	TLS      bool
	CAFile   string
	CertFile string
	KeyFile  string
	// Targets in the settings' order.
	Targets []DockerTarget
}

// DockerTarget selects the source's containers: Kind is compose_project,
// compose_service, name or label (domain.LadoLogTargetKind).
type DockerTarget struct {
	ID   string
	Kind string
	// Value is the compose project, the compose service, the container name,
	// or the label (key or key=value).
	Value string
	// ComposeProject narrows a compose_service target to one compose project.
	ComposeProject string
	// Service overrides the service name of the containers it selects.
	Service string
}

// DockerSource is a LogSource on the Docker Engine API. Discover lives in
// docker_discover.go and Follow in docker_logs.go.
type DockerSource struct {
	cfg DockerConfig
	// base is the scheme and host every request goes to.
	base url.URL
	// client makes the short requests; stream the log streams, which only
	// ctx ends.
	client *http.Client
	stream *http.Client
}

// DockerCheck is what a connection check found: the Engine's versions and
// the instances each target selects.
type DockerCheck struct {
	EngineVersion string
	APIVersion    string
	// Insecure is whether the source talks to the Engine without TLS.
	Insecure  bool
	Instances []Instance
}

// dockerStatusError is an Engine answer outside 2xx.
type dockerStatusError struct {
	Status  int
	Message string
}

func (e *dockerStatusError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("docker engine answered %d", e.Status)
	}
	return fmt.Sprintf("docker engine answered %d: %s", e.Status, e.Message)
}

// dockerNotFound reports whether err is an Engine 404 (a container that is
// gone).
func dockerNotFound(err error) bool {
	var se *dockerStatusError
	return errors.As(err, &se) && se.Status == http.StatusNotFound
}

// errDockerRedirect is the error a redirect gets.
var errDockerRedirect = errors.New("docker engine redirect refused")

// NewDockerSource makes a source for one Engine. With TLS on it reads the CA
// and the client certificate now, and needs the CA.
func NewDockerSource(cfg DockerConfig) (*DockerSource, error) {
	hostPort, err := dockerEndpointHost(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	base := url.URL{Scheme: "http", Host: hostPort}
	transport := &http.Transport{
		// Proxy stays nil: production logs do not go through the
		// environment's proxy.
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: dockerDialTimeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   dockerDialTimeout,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: time.Second,
		ForceAttemptHTTP2:     false,
	}
	if cfg.TLS {
		host, _, _ := net.SplitHostPort(hostPort)
		tlsConfig, err := dockerTLSConfig(cfg.CAFile, cfg.CertFile, cfg.KeyFile, host)
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = tlsConfig
		base.Scheme = "https"
	}
	streamTransport := transport.Clone()
	transport.ResponseHeaderTimeout = dockerHeaderTimeout
	refuseRedirect := func(*http.Request, []*http.Request) error { return errDockerRedirect }
	s := &DockerSource{cfg: cfg, base: base}
	s.client = &http.Client{
		Transport:     &dockerReadOnlyTransport{base: transport, want: base},
		CheckRedirect: refuseRedirect,
		Timeout:       dockerRequestTimeout,
	}
	s.stream = &http.Client{
		Transport:     &dockerReadOnlyTransport{base: streamTransport, want: base},
		CheckRedirect: refuseRedirect,
	}
	return s, nil
}

// dockerEndpointHost reads host:port out of tcp://host:port. The domain
// validates endpoints before they are saved; this is the client's own guard.
func dockerEndpointHost(endpoint string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return "", fmt.Errorf("docker endpoint is not a URL")
	}
	if u.Scheme != "tcp" {
		return "", fmt.Errorf("docker endpoint must be tcp://host:port")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("docker endpoint must be tcp://host:port with nothing else")
	}
	host, port := u.Hostname(), u.Port()
	if host == "" || strings.HasPrefix(host, "-") {
		return "", fmt.Errorf("docker endpoint has no valid host")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("docker endpoint needs a port from 1 to 65535")
	}
	return u.Host, nil
}

// dockerReadOnlyTransport checks every request against the allow list again
// right before it goes out.
type dockerReadOnlyTransport struct {
	base http.RoundTripper
	want url.URL
}

func (t *dockerReadOnlyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := dockerAllowed(req, t.want); err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	return t.base.RoundTrip(req)
}

// dockerIDPattern is a container id: 12 to 64 lowercase hex digits.
var dockerIDPattern = regexp.MustCompile(`^[0-9a-f]{12,64}$`)

// dockerQueryKeys are the query keys each allowed path takes.
var dockerQueryKeys = map[string]map[string]bool{
	"/version":         {},
	"/containers/json": {"filters": true, "all": true},
	"inspect":          {},
	"logs":             {"follow": true, "stdout": true, "stderr": true, "timestamps": true, "since": true, "tail": true},
}

// dockerAllowed is the allow list: it refuses every request but a GET with
// no body to the configured host on one of the four read paths, with that
// path's query keys.
func dockerAllowed(req *http.Request, want url.URL) error {
	if req == nil || req.URL == nil {
		return errors.New("docker request refused: empty request")
	}
	if req.Method != http.MethodGet {
		return fmt.Errorf("docker request refused: method %q is not GET", req.Method)
	}
	if (req.Body != nil && req.Body != http.NoBody) || req.ContentLength > 0 {
		return errors.New("docker request refused: a GET has no body")
	}
	u := req.URL
	if u.Scheme != want.Scheme || u.Host != want.Host || (req.Host != "" && req.Host != want.Host) {
		return errors.New("docker request refused: not the configured host")
	}
	if u.User != nil || u.Opaque != "" || u.Fragment != "" || u.RawFragment != "" {
		return errors.New("docker request refused: unexpected URL parts")
	}
	if u.RawPath != "" || strings.Contains(u.Path, "%") || strings.Contains(u.Path, "..") || strings.Contains(u.Path, "//") {
		return errors.New("docker request refused: encoded or relative path")
	}
	if req.Header.Get("Upgrade") != "" {
		return errors.New("docker request refused: connection upgrade")
	}
	keys, ok := dockerPathKind(u.Path)
	if !ok {
		return fmt.Errorf("docker request refused: path %q is not on the read-only list", u.Path)
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return errors.New("docker request refused: bad query")
	}
	for key, values := range query {
		if !keys[key] {
			return fmt.Errorf("docker request refused: query key %q", key)
		}
		if len(values) != 1 || !dockerQueryValueOK(key, values[0]) {
			return fmt.Errorf("docker request refused: query value for %q", key)
		}
	}
	return nil
}

// dockerPathKind gives the query keys of an allowed path.
func dockerPathKind(path string) (map[string]bool, bool) {
	switch path {
	case "/version", "/containers/json":
		return dockerQueryKeys[path], true
	}
	rest, ok := strings.CutPrefix(path, "/containers/")
	if !ok {
		return nil, false
	}
	id, action, ok := strings.Cut(rest, "/")
	if !ok || !dockerIDPattern.MatchString(id) {
		return nil, false
	}
	switch action {
	case "json":
		return dockerQueryKeys["inspect"], true
	case "logs":
		return dockerQueryKeys["logs"], true
	}
	return nil, false
}

// dockerQueryValueOK checks a query value's shape.
func dockerQueryValueOK(key, value string) bool {
	switch key {
	case "all", "follow", "stdout", "stderr", "timestamps":
		switch value {
		case "0", "1", "true", "false":
			return true
		}
		return false
	case "tail":
		if value == "all" {
			return true
		}
		_, err := strconv.ParseUint(value, 10, 32)
		return err == nil
	case "since":
		secs, nanos, _ := strings.Cut(value, ".")
		if _, err := strconv.ParseUint(secs, 10, 63); err != nil {
			return false
		}
		if nanos == "" {
			return !strings.Contains(value, ".")
		}
		_, err := strconv.ParseUint(nanos, 10, 32)
		return err == nil && len(nanos) <= 9
	case "filters":
		var filters map[string][]string
		return json.Unmarshal([]byte(value), &filters) == nil
	}
	return false
}

// dockerGet is the one place the package makes a request: always a GET with
// no body, to the configured Engine, on an allowed path. A log stream
// (/containers/{id}/logs) uses the client without a response timeout, so
// only ctx ends it. An answer outside 2xx is a *dockerStatusError, with the
// body closed. The caller closes the body of a 2xx answer.
func (s *DockerSource) dockerGet(ctx context.Context, path string, query url.Values) (*http.Response, error) {
	u := s.base
	u.Path = path
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("docker request: %w", err)
	}
	if err := dockerAllowed(req, s.base); err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "lado-log-watch")
	client := s.client
	if strings.HasSuffix(path, "/logs") {
		client = s.stream
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, dockerMaxErrorBody))
		var body struct {
			Message string `json:"message"`
		}
		msg := ""
		if json.Unmarshal(data, &body) == nil {
			msg = body.Message
		}
		return nil, &dockerStatusError{Status: resp.StatusCode, Message: dockerClip(msg, 300)}
	}
	return resp, nil
}

// dockerGetJSON makes a short dockerGet and decodes its JSON answer.
func (s *DockerSource) dockerGetJSON(ctx context.Context, path string, query url.Values, out any) error {
	resp, err := s.dockerGet(ctx, path, query)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, dockerMaxJSON+1))
	if err != nil {
		return fmt.Errorf("read docker answer: %w", err)
	}
	if len(data) > dockerMaxJSON {
		return errors.New("docker answer is too large")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode docker answer: %w", err)
	}
	return nil
}

// dockerContainer is what the package reads from GET /containers/{id}/json.
type dockerContainer struct {
	ID        string
	Name      string
	Image     string
	Labels    map[string]string
	Tty       bool
	Running   bool
	StartedAt time.Time
}

// dockerInspect reads one container. A container that is gone is a
// *dockerStatusError with status 404 (dockerNotFound).
func (s *DockerSource) dockerInspect(ctx context.Context, id string) (dockerContainer, error) {
	if !dockerIDPattern.MatchString(id) {
		return dockerContainer{}, fmt.Errorf("docker container id %q is not hex", id)
	}
	var raw struct {
		ID     string `json:"Id"`
		Name   string `json:"Name"`
		Config struct {
			Image  string            `json:"Image"`
			Labels map[string]string `json:"Labels"`
			Tty    bool              `json:"Tty"`
		} `json:"Config"`
		State struct {
			Running   bool   `json:"Running"`
			StartedAt string `json:"StartedAt"`
		} `json:"State"`
	}
	if err := s.dockerGetJSON(ctx, "/containers/"+id+"/json", nil, &raw); err != nil {
		return dockerContainer{}, err
	}
	c := dockerContainer{
		ID:      raw.ID,
		Name:    strings.TrimPrefix(raw.Name, "/"),
		Image:   raw.Config.Image,
		Labels:  raw.Config.Labels,
		Tty:     raw.Config.Tty,
		Running: raw.State.Running,
	}
	if c.ID == "" {
		c.ID = id
	}
	if t, err := time.Parse(time.RFC3339Nano, raw.State.StartedAt); err == nil && t.Year() > 1 {
		c.StartedAt = t
	}
	return c, nil
}

// CheckDocker checks a source: the Engine's versions (GET /version) and the
// running instances its targets select. A connection failure is an error;
// the caller shows it as a failed check.
func CheckDocker(ctx context.Context, cfg DockerConfig) (DockerCheck, error) {
	s, err := NewDockerSource(cfg)
	if err != nil {
		return DockerCheck{}, err
	}
	var version struct {
		Version    string `json:"Version"`
		APIVersion string `json:"ApiVersion"`
	}
	if err := s.dockerGetJSON(ctx, "/version", nil, &version); err != nil {
		return DockerCheck{}, err
	}
	instances, err := s.Discover(ctx)
	if err != nil {
		return DockerCheck{}, err
	}
	return DockerCheck{
		EngineVersion: version.Version,
		APIVersion:    version.APIVersion,
		Insecure:      !cfg.TLS,
		Instances:     instances,
	}, nil
}

// dockerClip cuts s to n bytes on a rune boundary.
func dockerClip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
