package domain

// LADO fork: production log watch (issue #87). LADO follows the container
// logs of a project's production Docker hosts through the Docker Engine API,
// groups error and exception blocks into one incident each, keys them by a
// service-level fingerprint, and turns a new fingerprint into an issue, a LADO
// task with Source LadoTaskSourceLog and, within caps, a plan request. The
// settings, sources, targets, read cursors, fingerprints and per-instance
// counts live in migrations_lado/0017_lado_log_watch.sql. Logs themselves are
// never stored.

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// LadoTaskSourceLog is the LadoTask.Source of a task the log watch created.
// A task the developer created or imported has an empty source.
const LadoTaskSourceLog = "log"

// ErrLadoLogSourceNotFound and ErrLadoLogFingerprintNotFound mark a source or
// fingerprint the store does not have.
var (
	ErrLadoLogSourceNotFound      = errors.New("LADO log source not found")
	ErrLadoLogFingerprintNotFound = errors.New("LADO log fingerprint not found")
)

// LadoLogSourceID identifies a log source, one Docker host of a project.
type LadoLogSourceID string

// LadoLogTargetID identifies one target, the containers a source selects.
type LadoLogTargetID string

// LadoLogFingerprintID identifies a stored fingerprint.
type LadoLogFingerprintID int64

// LadoLogSourceKindDockerTCP is the one source kind: the Docker Engine API
// over tcp://host:port, the way a project's CI deploys with DOCKER_HOST. An
// empty kind in the input means this kind.
const LadoLogSourceKindDockerTCP = "docker_tcp"

// LadoLogTargetKind is how a target selects containers.
type LadoLogTargetKind string

// The target kinds.
const (
	// LadoLogTargetComposeProject selects the containers labelled
	// com.docker.compose.project=<value>.
	LadoLogTargetComposeProject LadoLogTargetKind = "compose_project"
	// LadoLogTargetComposeService selects the containers labelled
	// com.docker.compose.service=<value>, and also of ComposeProject when it
	// is set.
	LadoLogTargetComposeService LadoLogTargetKind = "compose_service"
	// LadoLogTargetName selects the container with exactly this name.
	LadoLogTargetName LadoLogTargetKind = "name"
	// LadoLogTargetLabel selects the containers with a label key, or a
	// key=value pair.
	LadoLogTargetLabel LadoLogTargetKind = "label"
)

// The defaults of a project's log watch settings.
const (
	LadoLogDefaultHourlyIssueCap         = 5
	LadoLogDefaultAutoPlan               = true
	LadoLogDefaultAutoPlanDailyCap       = 5
	LadoLogDefaultAutoPlanMinCount       = 1
	LadoLogDefaultDisconnectAlertMinutes = 10
)

// The limits ValidateLadoLogWatchInput holds the input to.
const (
	LadoLogMaxSources            = 10
	LadoLogMaxTargetsPerSource   = 10
	LadoLogMaxPatterns           = 20
	LadoLogMaxPatternLength      = 500
	LadoLogMaxHourlyIssueCap     = 50
	LadoLogMaxAutoPlanDailyCap   = 50
	LadoLogMaxAutoPlanMinCount   = 10000
	LadoLogMaxDisconnectMinutes  = 1440
	LadoLogMaxDisplayNameRunes   = 64
	LadoLogMaxValueLength        = 256
	LadoLogMaxHostLength         = 253
	LadoLogMaxRepositoryLength   = 512
	ladoLogMaxIDLength           = 64
	ladoLogMaxComposeNameLength  = 128
	ladoLogMaxServiceNameLength  = 128
	ladoLogMaxEndpointTextLength = 300
)

// LadoLogWatchSettings are a project's log watch settings with its sources
// and their targets, in the order the developer set them.
type LadoLogWatchSettings struct {
	ProjectID ProjectID
	Enabled   bool
	// Repository is the canonical id of the repository the issues go to. It
	// may be empty when the project has one repository.
	Repository string
	// Include and Exclude are RE2 patterns: a line matching Include is an
	// error line besides the built-in rules, one matching Exclude never is.
	Include []string
	Exclude []string
	// HourlyIssueCap is how many issues the log watch creates in the last 60
	// minutes at most.
	HourlyIssueCap int
	// AutoPlan requests a plan for a new incident's task, at most
	// AutoPlanDailyCap times in the last 24 hours and only for a fingerprint
	// seen AutoPlanMinCount times.
	AutoPlan         bool
	AutoPlanDailyCap int
	AutoPlanMinCount int
	// DisconnectAlertMinutes is how long a source may stay disconnected
	// before LADO tells the orchestrator once.
	DisconnectAlertMinutes int
	// BurstNotifiedAt is when LADO last told of the hourly cap, zero when it
	// never did. The server sets it.
	BurstNotifiedAt time.Time
	Sources         []LadoLogSource
	UpdatedAt       time.Time
}

// DefaultLadoLogWatchSettings are the settings of a project that never saved
// any: off, with the default caps.
func DefaultLadoLogWatchSettings(project ProjectID) LadoLogWatchSettings {
	return LadoLogWatchSettings{
		ProjectID:              project,
		HourlyIssueCap:         LadoLogDefaultHourlyIssueCap,
		AutoPlan:               LadoLogDefaultAutoPlan,
		AutoPlanDailyCap:       LadoLogDefaultAutoPlanDailyCap,
		AutoPlanMinCount:       LadoLogDefaultAutoPlanMinCount,
		DisconnectAlertMinutes: LadoLogDefaultDisconnectAlertMinutes,
		Include:                []string{},
		Exclude:                []string{},
		Sources:                []LadoLogSource{},
	}
}

// LadoLogSource is one Docker host a project reads logs from.
type LadoLogSource struct {
	ID LadoLogSourceID
	// Kind is LadoLogSourceKindDockerTCP.
	Kind string
	// DisplayName is the only name of the host issues and notices show.
	DisplayName string
	// Endpoint is tcp://host:port.
	Endpoint string
	// TLS connects with the imported CA and, when one is imported, the client
	// certificate.
	TLS bool
	// CertKey names the directory of the source's certificate copies under
	// the credential store. The server sets it.
	CertKey string
	Paused  bool
	// DownSince is when the source lost its connection, zero while it is
	// connected; DownNotifiedAt is when LADO told of it. The server sets
	// both.
	DownSince      time.Time
	DownNotifiedAt time.Time
	Targets        []LadoLogTarget
}

// LadoLogTarget is one set of containers a source follows.
type LadoLogTarget struct {
	ID       LadoLogTargetID
	SourceID LadoLogSourceID
	Kind     LadoLogTargetKind
	// Value is the compose project, compose service, container name or label
	// (key or key=value) the kind selects by.
	Value string
	// ComposeProject narrows a compose_service target to one compose project.
	ComposeProject string
	// Service overrides the service name the fingerprints use.
	Service string
	Paused  bool
}

// LadoLogWatchInput is what the developer changes of a project's settings.
// A new source or target has an empty ID; the server gives it one. Values the
// server keeps (DownSince, CertKey, BurstNotifiedAt) are not part of it.
type LadoLogWatchInput struct {
	Enabled                bool
	Repository             string
	Include                []string
	Exclude                []string
	HourlyIssueCap         int
	AutoPlan               bool
	AutoPlanDailyCap       int
	AutoPlanMinCount       int
	DisconnectAlertMinutes int
	Sources                []LadoLogSourceInput
}

// LadoLogSourceInput is a source as the developer sets it.
type LadoLogSourceInput struct {
	ID          LadoLogSourceID
	Kind        string
	DisplayName string
	Endpoint    string
	TLS         bool
	Paused      bool
	Targets     []LadoLogTargetInput
}

// LadoLogTargetInput is a target as the developer sets it.
type LadoLogTargetInput struct {
	ID             LadoLogTargetID
	Kind           LadoLogTargetKind
	Value          string
	ComposeProject string
	Service        string
	Paused         bool
}

// LadoLogTLSImport names the files a source's TLS material is copied from.
// CertPath and KeyPath go together or not at all.
type LadoLogTLSImport struct {
	CAPath   string
	CertPath string
	KeyPath  string
}

// LadoLogTLSStored tells which TLS copies LADO keeps for a source; never
// their content or paths.
type LadoLogTLSStored struct {
	CA   bool
	Cert bool
	Key  bool
}

// LadoLogAutoPlanHold is why LADO did not request a plan for a fingerprint's
// task; empty when it did or has not decided yet.
type LadoLogAutoPlanHold string

// The reasons a plan request waits.
const (
	// LadoLogAutoPlanHeldCap: the daily cap was reached.
	LadoLogAutoPlanHeldCap LadoLogAutoPlanHold = "cap"
	// LadoLogAutoPlanHeldMinCount: the fingerprint was seen fewer times than
	// the minimum; LADO requests the plan once it reaches it.
	LadoLogAutoPlanHeldMinCount LadoLogAutoPlanHold = "min_count"
	// LadoLogAutoPlanHeldOff: automatic plan requests are off.
	LadoLogAutoPlanHeldOff LadoLogAutoPlanHold = "off"
	// LadoLogAutoPlanHeldFailed: the plan request failed.
	LadoLogAutoPlanHeldFailed LadoLogAutoPlanHold = "failed"
)

// LadoLogFingerprint is one incident kind of a project: what LADO counted of
// it and the task and issue it leads to. The fingerprint is per service, so
// replicas on several hosts share one.
type LadoLogFingerprint struct {
	ID          LadoLogFingerprintID
	ProjectID   ProjectID
	Fingerprint string
	Service     string
	// ExceptionKind and Location are cleaned for titles and notices.
	ExceptionKind string
	Location      string
	Count         int
	FirstSeenAt   time.Time
	LastSeenAt    time.Time
	// TaskID is the task LADO created for the fingerprint, empty before it
	// did or after that task was deleted unconfirmed. IssueRef is the task's
	// issue once LADO confirmed it; PreviousIssueRef the closed issue a
	// recurrence links to.
	TaskID           LadoTaskID
	IssueRef         IssueID
	PreviousIssueRef IssueID
	// IssueCreatedAt is when LADO last started creating the fingerprint's
	// issue; the hourly cap counts it.
	IssueCreatedAt      time.Time
	AutoPlanRequestedAt time.Time
	AutoPlanHeld        LadoLogAutoPlanHold
	Ignored             bool
	IgnoredAt           time.Time
	// Instances are the counts per instance, by instance name.
	Instances []LadoLogInstanceCount
	// ServiceInstances is how many instances of the service the log watch
	// follows now, across the project's sources: the n of "n개 중 m개". It is
	// not stored; the runner fills it in, 0 when none is attached.
	ServiceInstances int
}

// LadoLogInstanceCount is how often one instance showed a fingerprint.
type LadoLogInstanceCount struct {
	// Instance is "<source display name>/<container name>".
	Instance string
	// Image is the image tag the instance ran when it last showed it.
	Image      string
	Count      int
	LastSeenAt time.Time
}

// LadoLogOccurrenceDelta is what one instance added to a fingerprint since
// the last write. It creates the fingerprint when it is new.
type LadoLogOccurrenceDelta struct {
	Project     ProjectID
	Fingerprint string
	Service     string
	Kind        string
	Location    string
	Instance    string
	Image       string
	Delta       int
	// FirstSeen is the first of the occurrences, LastSeen the last; a zero
	// FirstSeen is LastSeen.
	FirstSeen time.Time
	LastSeen  time.Time
}

// LadoLogCursor is where reading one instance of a target goes on: the
// Docker timestamp, on the host's clock, of the last line LADO handled.
type LadoLogCursor struct {
	TargetID  LadoLogTargetID
	Instance  string
	Since     time.Time
	UpdatedAt time.Time
}

// The states of a source's follower.
const (
	LadoLogSourceStateOff        = "off"
	LadoLogSourceStateConnecting = "connecting"
	LadoLogSourceStateWatching   = "watching"
	LadoLogSourceStateBackoff    = "backoff"
	LadoLogSourceStateError      = "error"
)

// LadoLogSourceStatus is how a source's follower is doing. Only the running
// daemon knows it.
type LadoLogSourceStatus struct {
	SourceID LadoLogSourceID
	State    string
	Detail   string
	// Insecure is true for a source without TLS: the Engine API is
	// unauthenticated and the logs cross the network in plain text.
	Insecure  bool
	DownSince time.Time
	Targets   []LadoLogTargetStatus
}

// LadoLogTargetStatus is how many instances of a target LADO follows.
type LadoLogTargetStatus struct {
	TargetID   LadoLogTargetID
	Instances  int
	LastLineAt time.Time
}

// LadoLogWatchView is what the settings screen reads.
type LadoLogWatchView struct {
	Settings LadoLogWatchSettings
	// LadoMode is the project's LADO mode; the log watch connects only while
	// it is on.
	LadoMode  bool
	TLSStored map[LadoLogSourceID]LadoLogTLSStored
	Statuses  []LadoLogSourceStatus
}

// LadoLogWatchCheck is the result of a connection check. A failed connection
// is OK false with Detail, not an error.
type LadoLogWatchCheck struct {
	OK            bool
	Detail        string
	EngineVersion string
	APIVersion    string
	Insecure      bool
	Instances     []LadoLogInstance
}

// LadoLogInstance is a container a target matches.
type LadoLogInstance struct {
	TargetID string
	Name     string
	ID       string
	Image    string
	Service  string
	State    string
}

// LadoLogCIImport is what LADO found in a project's .gitlab-ci.yml files.
// It is a list of candidates; nothing is saved.
type LadoLogCIImport struct {
	Files []LadoLogCIFile
}

// LadoLogCIFile is the candidates of one file.
type LadoLogCIFile struct {
	// Repo is the repository folder relative to a workspace project, empty
	// for a single repository project.
	Repo            string
	Path            string
	Hosts           []LadoLogCIHost
	ComposeProjects []string
	Notes           []string
}

// LadoLogCIHost is one endpoint candidate. Unresolved holds the variable
// reference LADO could not resolve, such as $PROD_HOST, and the developer
// fills it in.
type LadoLogCIHost struct {
	Variable    string
	DisplayName string
	Endpoint    string
	Unresolved  string
}

// LadoLogWatchInvalidError is a refused log watch input. Field names the
// value, such as "sources[0].endpoint".
type LadoLogWatchInvalidError struct {
	Field   string
	Message string
}

func (e *LadoLogWatchInvalidError) Error() string {
	return fmt.Sprintf("invalid log watch setting %s: %s", e.Field, e.Message)
}

func ladoLogInvalid(field, format string, args ...any) error {
	return &LadoLogWatchInvalidError{Field: field, Message: fmt.Sprintf(format, args...)}
}

var (
	ladoLogHostnamePattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)
	ladoLogComposePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	ladoLogLabelPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_./-]*(=[A-Za-z0-9_.:/-]*)?$`)
	ladoLogServicePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_./-]*$`)
	ladoLogIDPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
)

// ValidateLadoLogWatchInput checks a settings input before it is saved. It
// returns a *LadoLogWatchInvalidError naming the first refused value.
func ValidateLadoLogWatchInput(in LadoLogWatchInput) error {
	if len(in.Repository) > LadoLogMaxRepositoryLength || ladoLogHasControl(in.Repository) {
		return ladoLogInvalid("repository", "저장소 id 가 올바르지 않습니다")
	}
	if err := ladoLogValidatePatterns("include", in.Include); err != nil {
		return err
	}
	if err := ladoLogValidatePatterns("exclude", in.Exclude); err != nil {
		return err
	}
	for _, r := range []struct {
		field    string
		value    int
		min, max int
	}{
		{"hourlyIssueCap", in.HourlyIssueCap, 1, LadoLogMaxHourlyIssueCap},
		{"autoPlanDailyCap", in.AutoPlanDailyCap, 1, LadoLogMaxAutoPlanDailyCap},
		{"autoPlanMinCount", in.AutoPlanMinCount, 1, LadoLogMaxAutoPlanMinCount},
		{"disconnectAlertMinutes", in.DisconnectAlertMinutes, 1, LadoLogMaxDisconnectMinutes},
	} {
		if r.value < r.min || r.value > r.max {
			return ladoLogInvalid(r.field, "%d–%d 사이여야 합니다", r.min, r.max)
		}
	}
	if len(in.Sources) > LadoLogMaxSources {
		return ladoLogInvalid("sources", "출처는 %d개까지입니다", LadoLogMaxSources)
	}
	sourceIDs := map[LadoLogSourceID]bool{}
	targetIDs := map[LadoLogTargetID]bool{}
	names := map[string]bool{}
	endpoints := map[string]bool{}
	for i, src := range in.Sources {
		field := fmt.Sprintf("sources[%d]", i)
		if src.ID != "" {
			if !ladoLogValidID(string(src.ID)) {
				return ladoLogInvalid(field+".id", "id 가 올바르지 않습니다")
			}
			if sourceIDs[src.ID] {
				return ladoLogInvalid(field+".id", "같은 출처 id 가 두 번 있습니다")
			}
			sourceIDs[src.ID] = true
		}
		if src.Kind != "" && src.Kind != LadoLogSourceKindDockerTCP {
			return ladoLogInvalid(field+".kind", "지원하는 출처 종류는 %s 뿐입니다", LadoLogSourceKindDockerTCP)
		}
		name := strings.TrimSpace(src.DisplayName)
		if name == "" || name != src.DisplayName || utf8.RuneCountInString(name) > LadoLogMaxDisplayNameRunes ||
			ladoLogHasControl(name) || strings.ContainsAny(name, "`/") {
			return ladoLogInvalid(field+".displayName", "표시 이름은 앞뒤 공백·제어 문자·`·/ 없이 1–%d자여야 합니다", LadoLogMaxDisplayNameRunes)
		}
		if names[name] {
			return ladoLogInvalid(field+".displayName", "같은 표시 이름의 출처가 이미 있습니다")
		}
		names[name] = true
		if _, _, err := ParseLadoLogEndpoint(src.Endpoint); err != nil {
			return ladoLogInvalid(field+".endpoint", "%s", err.Error())
		}
		key := strings.ToLower(src.Endpoint)
		if endpoints[key] {
			return ladoLogInvalid(field+".endpoint", "같은 엔드포인트의 출처가 이미 있습니다")
		}
		endpoints[key] = true
		if len(src.Targets) > LadoLogMaxTargetsPerSource {
			return ladoLogInvalid(field+".targets", "출처마다 대상은 %d개까지입니다", LadoLogMaxTargetsPerSource)
		}
		for j, target := range src.Targets {
			tfield := fmt.Sprintf("%s.targets[%d]", field, j)
			if target.ID != "" {
				if !ladoLogValidID(string(target.ID)) {
					return ladoLogInvalid(tfield+".id", "id 가 올바르지 않습니다")
				}
				if targetIDs[target.ID] {
					return ladoLogInvalid(tfield+".id", "같은 대상 id 가 두 번 있습니다")
				}
				targetIDs[target.ID] = true
			}
			if err := ladoLogValidateTarget(tfield, target); err != nil {
				return err
			}
		}
	}
	return nil
}

func ladoLogValidateTarget(field string, t LadoLogTargetInput) error {
	switch t.Kind {
	case LadoLogTargetComposeProject, LadoLogTargetComposeService, LadoLogTargetName:
		if len(t.Value) > ladoLogMaxComposeNameLength || !ladoLogComposePattern.MatchString(t.Value) {
			return ladoLogInvalid(field+".value", "영문·숫자로 시작하고 영문·숫자·_ . - 만 쓸 수 있습니다")
		}
	case LadoLogTargetLabel:
		if len(t.Value) > LadoLogMaxValueLength || !ladoLogLabelPattern.MatchString(t.Value) {
			return ladoLogInvalid(field+".value", "라벨은 key 또는 key=value 꼴이어야 합니다(영문·숫자·_ . / - , 값에는 : 도)")
		}
	default:
		return ladoLogInvalid(field+".kind", "대상 종류는 compose_project·compose_service·name·label 중 하나여야 합니다")
	}
	if t.ComposeProject != "" {
		if t.Kind != LadoLogTargetComposeService {
			return ladoLogInvalid(field+".composeProject", "compose 프로젝트는 compose_service 대상에만 씁니다")
		}
		if len(t.ComposeProject) > ladoLogMaxComposeNameLength || !ladoLogComposePattern.MatchString(t.ComposeProject) {
			return ladoLogInvalid(field+".composeProject", "영문·숫자로 시작하고 영문·숫자·_ . - 만 쓸 수 있습니다")
		}
	}
	if t.Service != "" && (len(t.Service) > ladoLogMaxServiceNameLength || !ladoLogServicePattern.MatchString(t.Service)) {
		return ladoLogInvalid(field+".service", "서비스 이름은 영문·숫자로 시작하고 영문·숫자·_ . / - 만 쓸 수 있습니다")
	}
	return nil
}

func ladoLogValidatePatterns(field string, patterns []string) error {
	if len(patterns) > LadoLogMaxPatterns {
		return ladoLogInvalid(field, "정규식은 %d개까지입니다", LadoLogMaxPatterns)
	}
	for i, p := range patterns {
		f := fmt.Sprintf("%s[%d]", field, i)
		if p == "" || len(p) > LadoLogMaxPatternLength {
			return ladoLogInvalid(f, "정규식은 1–%d자여야 합니다", LadoLogMaxPatternLength)
		}
		if _, err := regexp.Compile(p); err != nil {
			return ladoLogInvalid(f, "정규식을 컴파일할 수 없습니다: %v", err)
		}
	}
	return nil
}

// ParseLadoLogEndpoint checks a source endpoint, tcp://host:port, and returns
// its host (an IPv6 host without brackets) and port. It refuses other
// schemes, user information, a path, query or fragment, a missing or out of
// range port and a host that starts with '-'.
func ParseLadoLogEndpoint(endpoint string) (string, int, error) {
	if endpoint == "" {
		return "", 0, fmt.Errorf("엔드포인트를 tcp://host:port 꼴로 적으세요")
	}
	if len(endpoint) > ladoLogMaxEndpointTextLength || strings.ContainsAny(endpoint, "?#%\\ \t") || ladoLogHasControl(endpoint) {
		return "", 0, fmt.Errorf("엔드포인트는 tcp://host:port 만 받습니다(경로·쿼리·조각·인코딩 없이)")
	}
	scheme, rest, ok := strings.Cut(endpoint, "://")
	if !ok {
		return "", 0, fmt.Errorf("엔드포인트는 tcp://host:port 꼴이어야 합니다")
	}
	if !strings.EqualFold(scheme, "tcp") {
		return "", 0, fmt.Errorf("%s:// 는 받지 않습니다 — 이번에는 tcp://host:port 만 받습니다", strings.ToLower(scheme))
	}
	if strings.Contains(rest, "@") {
		return "", 0, fmt.Errorf("엔드포인트에 사용자 정보를 넣지 마세요")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", 0, fmt.Errorf("엔드포인트는 tcp://host:port 꼴이어야 합니다")
	}
	if u.Path != "" && u.Path != "/" {
		return "", 0, fmt.Errorf("엔드포인트에 경로를 넣지 마세요")
	}
	host, portText := u.Hostname(), u.Port()
	if portText == "" || !strings.HasSuffix(u.Host, ":"+portText) {
		return "", 0, fmt.Errorf("엔드포인트에 포트를 적으세요(예: tcp://host.example.test:2376)")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("포트는 1–65535 사이여야 합니다")
	}
	switch {
	case host == "":
		return "", 0, fmt.Errorf("엔드포인트에 호스트를 적으세요")
	case strings.HasPrefix(host, "-"):
		return "", 0, fmt.Errorf("호스트는 '-' 로 시작할 수 없습니다")
	case strings.HasPrefix(u.Host, "["):
		if ip := net.ParseIP(host); ip == nil || ip.To4() != nil && !strings.Contains(host, ":") {
			return "", 0, fmt.Errorf("대괄호 안에는 IPv6 주소만 씁니다")
		}
	case strings.Contains(host, ":"):
		return "", 0, fmt.Errorf("IPv6 주소는 대괄호로 감싸세요")
	case len(host) > LadoLogMaxHostLength || !ladoLogHostnamePattern.MatchString(host):
		return "", 0, fmt.Errorf("호스트 이름이 올바르지 않습니다")
	}
	return host, port, nil
}

func ladoLogValidID(id string) bool {
	return len(id) <= ladoLogMaxIDLength && ladoLogIDPattern.MatchString(id)
}

func ladoLogHasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return !utf8.ValidString(s)
}

// The plan parts of the incident notices.
const (
	LadoLogPlanRequested = "계획 요청됨"
	LadoLogPlanHeldCap   = "보류(상한)"
	LadoLogPlanOff       = "자동 계획 꺼짐"
	LadoLogPlanFailed    = "보류(계획 요청 실패)"
)

// LadoLogPlanHeldMinCount is the plan part of a notice for a fingerprint
// seen fewer than min times.
func LadoLogPlanHeldMinCount(min int) string { return fmt.Sprintf("보류(%d회 미만)", min) }

// The log watch notice lines (docs/LJY-PRD/TDD.md §5.1). The exception kind
// and display names come from production logs and settings, so they are
// cleaned to one line without backticks before they go in.
const (
	ladoNoticeLogIncident   = "서버 오류 감지 — #%d (%s, %d회, %s)"
	ladoNoticeLogRecurred   = "서버 오류 감지 — #%d (%s, 이전 #%d, %s)"
	ladoNoticeLogBurst      = "오류 폭주 — 시간당 이슈 상한 %d개에 닿아 이슈 없이 기록합니다"
	ladoNoticeLogSourceDown = "로그 출처 끊김 — %s, %d분"
	ladoLogNoticeKindRunes  = 120
)

// LadoNoticeLogIncident is the line for a new incident's issue: its kind,
// how often it was seen and what became of the plan request (LadoLogPlan*).
func LadoNoticeLogIncident(issue int, kind string, count int, plan string) string {
	return ladoNoticeLine(ladoNoticeLogIncident, issue, ladoLogNoticeKind(kind), count, ladoLogNoticeText(plan))
}

// LadoNoticeLogRecurred is the line for a new issue of a fingerprint whose
// earlier issue was closed.
func LadoNoticeLogRecurred(issue, previous int, kind string, plan string) string {
	return ladoNoticeLine(ladoNoticeLogRecurred, issue, ladoLogNoticeKind(kind), previous, ladoLogNoticeText(plan))
}

// LadoNoticeLogBurst is the line for a project that reached its hourly issue
// cap.
func LadoNoticeLogBurst(cap int) string { return ladoNoticeLine(ladoNoticeLogBurst, cap) }

// LadoNoticeLogSourceDown is the line for a source disconnected for minutes.
func LadoNoticeLogSourceDown(name string, minutes int) string {
	return ladoNoticeLine(ladoNoticeLogSourceDown, ladoLogNoticeText(name), minutes)
}

// ladoLogNoticeKind keeps the characters an exception kind or location may
// have in a title, and cuts it.
func ladoLogNoticeKind(kind string) string {
	var b strings.Builder
	for _, r := range kind {
		if r < utf8.RuneSelf && (r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("_.:$<>/-", r)) {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) > ladoLogNoticeKindRunes {
		out = out[:ladoLogNoticeKindRunes]
	}
	if out == "" {
		return "오류"
	}
	return out
}

// ladoLogNoticeText makes s one line without backticks.
func ladoLogNoticeText(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '`' || unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}
