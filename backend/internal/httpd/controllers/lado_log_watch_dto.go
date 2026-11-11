package controllers

// LADO fork: DTOs of the production log watch (issue #87). No response
// carries a TLS certificate, a key or the path of either: a source tells only
// which copies LADO keeps (tlsStored).

import "time"

// LadoLogSourceIDParam names one log source of a project.
type LadoLogSourceIDParam struct {
	ProjectID string `path:"projectId" description:"Project the log source belongs to."`
	SourceID  string `path:"sourceId" description:"Log source id."`
}

// LadoLogFingerprintIDParam names one fingerprint of a project.
type LadoLogFingerprintIDParam struct {
	ProjectID     string `path:"projectId" description:"Project the fingerprint belongs to."`
	FingerprintID string `path:"fingerprintId" description:"Fingerprint id."`
}

// LadoLogTLSStoredResponse tells which TLS copies LADO keeps for a source.
type LadoLogTLSStoredResponse struct {
	CA   bool `json:"ca" description:"A CA certificate copy is stored."`
	Cert bool `json:"cert" description:"A client certificate copy is stored."`
	Key  bool `json:"key" description:"A client key copy is stored."`
}

// LadoLogSourceStatusResponse is how a source's follower is doing in the
// running daemon.
type LadoLogSourceStatusResponse struct {
	State     string     `json:"state" enum:"off,connecting,watching,backoff,error" description:"off: LADO does not connect (the watch, the project's LADO mode or the source is off, or TLS lacks its CA); connecting; watching; backoff: it waits to connect again; error."`
	Detail    string     `json:"detail,omitempty" description:"What the follower last reported, such as why it does not connect."`
	Insecure  bool       `json:"insecure" description:"The source does not use TLS: the Engine API is unauthenticated and the logs cross the network in plain text."`
	DownSince *time.Time `json:"downSince,omitempty" description:"Since when the source has been disconnected; omitted while it is connected."`
}

// LadoLogTargetStatusResponse is what the running daemon follows of a
// target.
type LadoLogTargetStatusResponse struct {
	Instances  int        `json:"instances" minimum:"0" description:"How many containers of the target LADO follows."`
	LastLineAt *time.Time `json:"lastLineAt,omitempty" description:"When the newest line LADO read of the target was written; omitted before one."`
}

// LadoLogTargetResponse is one set of containers a source follows.
type LadoLogTargetResponse struct {
	ID             string                      `json:"id"`
	Kind           string                      `json:"kind" enum:"compose_project,compose_service,name,label"`
	Value          string                      `json:"value" description:"The compose project, compose service, exact container name, or label key or key=value the kind selects by."`
	ComposeProject string                      `json:"composeProject,omitempty" description:"The compose project a compose_service target is narrowed to."`
	Service        string                      `json:"service,omitempty" description:"The service name the target's fingerprints use instead of the compose labels."`
	Repository     string                      `json:"repository,omitempty" description:"Canonical id of the repository the target's issues go to; omitted when the target uses the project's default repository."`
	Paused         bool                        `json:"paused"`
	Status         LadoLogTargetStatusResponse `json:"status"`
}

// LadoLogSourceResponse is one Docker host a project reads logs from.
type LadoLogSourceResponse struct {
	ID          string                      `json:"id"`
	Kind        string                      `json:"kind" enum:"docker_tcp"`
	DisplayName string                      `json:"displayName" description:"The only name of the host that issues and notices show."`
	Endpoint    string                      `json:"endpoint" description:"tcp://host:port of the Docker Engine API."`
	TLS         bool                        `json:"tls"`
	Paused      bool                        `json:"paused"`
	TLSStored   LadoLogTLSStoredResponse    `json:"tlsStored"`
	Status      LadoLogSourceStatusResponse `json:"status"`
	Targets     []LadoLogTargetResponse     `json:"targets"`
}

// LadoLogWatchResponse is a project's log watch settings with its sources,
// their targets and how the running daemon follows them.
type LadoLogWatchResponse struct {
	ProjectID              string                  `json:"projectId"`
	LadoMode               bool                    `json:"ladoMode" description:"The project's LADO mode; the log watch connects only while it is on."`
	Enabled                bool                    `json:"enabled"`
	Repository             string                  `json:"repository" description:"Canonical id of the repository the issues go to; empty when the project has one repository."`
	Include                []string                `json:"include" description:"RE2 patterns of lines that are errors besides the built-in rules."`
	Exclude                []string                `json:"exclude" description:"RE2 patterns of lines that are never errors."`
	HourlyIssueCap         int                     `json:"hourlyIssueCap" minimum:"1" maximum:"50"`
	AutoPlan               bool                    `json:"autoPlan" description:"Request a plan for a new incident's task."`
	AutoPlanDailyCap       int                     `json:"autoPlanDailyCap" minimum:"1" maximum:"50" description:"Automatic plan requests in the last 24 hours at most."`
	AutoPlanMinCount       int                     `json:"autoPlanMinCount" minimum:"1" maximum:"10000" description:"How often a fingerprint must be seen before LADO requests its plan."`
	DisconnectAlertMinutes int                     `json:"disconnectAlertMinutes" minimum:"1" maximum:"1440" description:"How long a source may stay disconnected before LADO tells the orchestrator once."`
	BurstNotifiedAt        *time.Time              `json:"burstNotifiedAt,omitempty" description:"When LADO last told of the hourly issue cap; omitted when it never did."`
	Sources                []LadoLogSourceResponse `json:"sources"`
	UpdatedAt              *time.Time              `json:"updatedAt,omitempty" description:"When the settings were last saved; omitted before they were."`
}

// UpdateLadoLogTargetRequest is a target as the developer sets it.
type UpdateLadoLogTargetRequest struct {
	ID             string `json:"id,omitempty" description:"The target's id; omitted for a new target."`
	Kind           string `json:"kind" enum:"compose_project,compose_service,name,label"`
	Value          string `json:"value"`
	ComposeProject string `json:"composeProject,omitempty"`
	Service        string `json:"service,omitempty"`
	Repository     string `json:"repository,omitempty" description:"Repository the target's issues go to; empty uses the project's default repository."`
	Paused         bool   `json:"paused,omitempty"`
}

// UpdateLadoLogSourceRequest is a source as the developer sets it.
type UpdateLadoLogSourceRequest struct {
	ID          string                       `json:"id,omitempty" description:"The source's id; omitted for a new source."`
	Kind        string                       `json:"kind,omitempty" enum:"docker_tcp" description:"docker_tcp when omitted."`
	DisplayName string                       `json:"displayName"`
	Endpoint    string                       `json:"endpoint" description:"tcp://host:port only: no other scheme, user information, path, query or fragment."`
	TLS         bool                         `json:"tls,omitempty"`
	Paused      bool                         `json:"paused,omitempty"`
	Targets     []UpdateLadoLogTargetRequest `json:"targets"`
}

// UpdateLadoLogWatchRequest replaces a project's log watch settings. A source
// or target left out is deleted with its read cursors, and a deleted
// source's TLS copies with it; fingerprints stay.
type UpdateLadoLogWatchRequest struct {
	Enabled                bool                         `json:"enabled"`
	Repository             string                       `json:"repository,omitempty"`
	Include                []string                     `json:"include"`
	Exclude                []string                     `json:"exclude"`
	HourlyIssueCap         int                          `json:"hourlyIssueCap" minimum:"1" maximum:"50"`
	AutoPlan               bool                         `json:"autoPlan"`
	AutoPlanDailyCap       int                          `json:"autoPlanDailyCap" minimum:"1" maximum:"50"`
	AutoPlanMinCount       int                          `json:"autoPlanMinCount" minimum:"1" maximum:"10000"`
	DisconnectAlertMinutes int                          `json:"disconnectAlertMinutes" minimum:"1" maximum:"1440"`
	Sources                []UpdateLadoLogSourceRequest `json:"sources"`
}

// ImportLadoLogTLSRequest names the local files LADO copies a source's TLS
// material from. The paths are read once and never returned.
type ImportLadoLogTLSRequest struct {
	CAPath   string `json:"caPath" description:"PEM CA certificate the Engine's server certificate is checked against."`
	CertPath string `json:"certPath,omitempty" description:"PEM client certificate; together with keyPath or not at all."`
	KeyPath  string `json:"keyPath,omitempty" description:"PEM client key without a passphrase; together with certPath or not at all."`
}

// CheckLadoLogSourceRequest names the saved source to check.
type CheckLadoLogSourceRequest struct {
	SourceID string `json:"sourceId"`
}

// LadoLogInstanceResponse is a container a target matches.
type LadoLogInstanceResponse struct {
	TargetID string `json:"targetId"`
	Name     string `json:"name"`
	ID       string `json:"id" description:"Container id."`
	Image    string `json:"image"`
	Service  string `json:"service" description:"The service name its fingerprints use."`
	State    string `json:"state,omitempty"`
}

// LadoLogWatchCheckResponse is the result of a connection check. A failed
// connection is ok false with detail.
type LadoLogWatchCheckResponse struct {
	OK            bool                      `json:"ok"`
	Detail        string                    `json:"detail,omitempty"`
	EngineVersion string                    `json:"engineVersion,omitempty"`
	APIVersion    string                    `json:"apiVersion,omitempty"`
	Insecure      bool                      `json:"insecure" description:"The source does not use TLS."`
	Instances     []LadoLogInstanceResponse `json:"instances" description:"The running containers each target matches."`
}

// LadoLogCIHostResponse is one endpoint candidate of a .gitlab-ci.yml file.
type LadoLogCIHostResponse struct {
	Variable    string `json:"variable,omitempty" description:"The *_HOST variable it came from."`
	DisplayName string `json:"displayName"`
	Endpoint    string `json:"endpoint" description:"tcp://host:port; empty while unresolved."`
	Unresolved  string `json:"unresolved,omitempty" description:"The variable reference LADO could not resolve, such as $PROD_HOST; the developer fills it in."`
}

// LadoLogCIFileResponse is the candidates of one .gitlab-ci.yml file.
type LadoLogCIFileResponse struct {
	Repo            string                  `json:"repo" description:"The repository folder relative to a workspace project; empty for a single repository project."`
	Repository      string                  `json:"repository,omitempty" description:"Canonical id of the repository the file belongs to; omitted for a file of the project root or one whose origin LADO cannot read."`
	Path            string                  `json:"path"`
	Hosts           []LadoLogCIHostResponse `json:"hosts"`
	ComposeProjects []string                `json:"composeProjects"`
	Notes           []string                `json:"notes"`
}

// LadoLogCIImportResponse is what LADO found in a project's .gitlab-ci.yml
// files. Nothing is saved.
type LadoLogCIImportResponse struct {
	Files []LadoLogCIFileResponse `json:"files"`
}

// LadoLogInstanceCountResponse is how often one instance showed a
// fingerprint.
type LadoLogInstanceCountResponse struct {
	Instance   string    `json:"instance" description:"<source display name>/<container name>."`
	Image      string    `json:"image,omitempty"`
	Count      int       `json:"count"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

// LadoLogFingerprintResponse is one incident kind of a project and what LADO
// counted of it.
type LadoLogFingerprintResponse struct {
	ID                  int64                          `json:"id"`
	ProjectID           string                         `json:"projectId"`
	Fingerprint         string                         `json:"fingerprint"`
	Service             string                         `json:"service"`
	ExceptionKind       string                         `json:"exceptionKind"`
	Location            string                         `json:"location,omitempty"`
	Count               int                            `json:"count"`
	FirstSeenAt         time.Time                      `json:"firstSeenAt"`
	LastSeenAt          time.Time                      `json:"lastSeenAt"`
	TaskID              string                         `json:"taskId,omitempty" description:"The task LADO created for it; omitted before it did or after that task was deleted unconfirmed."`
	IssueRef            string                         `json:"issueRef,omitempty"`
	PreviousIssueRef    string                         `json:"previousIssueRef,omitempty" description:"The closed issue a recurrence links to."`
	IssueCreatedAt      *time.Time                     `json:"issueCreatedAt,omitempty"`
	AutoPlanRequestedAt *time.Time                     `json:"autoPlanRequestedAt,omitempty"`
	AutoPlanHeld        string                         `json:"autoPlanHeld,omitempty" enum:"cap,min_count,off,failed" description:"Why LADO did not request a plan."`
	Ignored             bool                           `json:"ignored"`
	IgnoredAt           *time.Time                     `json:"ignoredAt,omitempty"`
	Instances           []LadoLogInstanceCountResponse `json:"instances"`
	ServiceInstances    int                            `json:"serviceInstances" minimum:"0" description:"How many instances of the service LADO follows now across the project's sources, the n of 'm of n instances'; 0 when none is attached."`
}

// LadoLogIncidentListResponse is a project's fingerprints: every one of an
// open task and the newest 200 others.
type LadoLogIncidentListResponse struct {
	Fingerprints []LadoLogFingerprintResponse `json:"fingerprints"`
}

// SetLadoLogFingerprintIgnoredRequest ignores a fingerprint or stops
// ignoring it.
type SetLadoLogFingerprintIgnoredRequest struct {
	Ignored bool `json:"ignored"`
}
