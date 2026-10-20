package controllers

// LADO fork: DTOs for repository connections and host tokens
// (docs/LJY-PRD/TDD.md §5.1 and §13). No response ever carries a token.

// CheckLadoConnectionRequest names a repository and the credentials to reach
// it.
type CheckLadoConnectionRequest struct {
	URL        string `json:"url" description:"The repository's SSH URL, such as git@github.com:owner/repo.git."`
	Token      string `json:"token,omitempty" description:"A new access token for the repository's host. Empty checks the host's existing token."`
	SSHKeyPath string `json:"sshKeyPath,omitempty" description:"Optional private key file for the repository. LADO stores the path, never the key."`
}

// CreateLadoConnectionRequest connects a repository: it links the registered
// project with the same repository, or clones it where placement says.
type CreateLadoConnectionRequest struct {
	URL               string `json:"url" description:"The repository's SSH URL, such as git@github.com:owner/repo.git."`
	Token             string `json:"token,omitempty" description:"A new access token for the repository's host. LADO stores it for the host. Empty keeps the host's existing token."`
	SSHKeyPath        string `json:"sshKeyPath,omitempty" description:"Optional private key file. The repository's core.sshCommand then uses that key."`
	Placement         string `json:"placement,omitempty" enum:"standalone,new_workspace,workspace" description:"Where to clone when no registered project has the repository: a project of its own (the default), the first repository of a new workspace, or one more repository of a registered workspace."`
	DestinationParent string `json:"destinationParent,omitempty" description:"Directory to clone into, or for new_workspace the directory to create the workspace folder in."`
	WorkspaceName     string `json:"workspaceName,omitempty" description:"Folder and project name of a new workspace."`
	WorkspaceID       string `json:"workspaceId,omitempty" description:"Registered workspace project to clone into."`
}

// LadoPermissionResponse is one checked token permission.
type LadoPermissionResponse struct {
	Name   string `json:"name" enum:"repository,issues,pull_requests,branch_rules,commit_statuses"`
	Status string `json:"status" enum:"ok,missing,unverified" description:"Unverified means the host cannot tell without trying the write."`
	Detail string `json:"detail,omitempty" description:"What is missing, or why it cannot be verified."`
}

// LadoTokenCheckResponse is what the host said about the token.
type LadoTokenCheckResponse struct {
	Entered       bool                     `json:"entered" description:"Whether the check used a newly entered token."`
	Stored        bool                     `json:"stored" description:"Whether LADO already keeps a token for the host."`
	Login         string                   `json:"login,omitempty" description:"Account the token belongs to."`
	Permissions   []LadoPermissionResponse `json:"permissions"`
	Problem       string                   `json:"problem,omitempty" description:"Why the token cannot be used, in English with the details."`
	ProblemCode   string                   `json:"problemCode,omitempty" enum:"token_missing,token_unusable,token_rejected,token_check_failed" description:"Why the token cannot be used, for the app to translate."`
	ProblemDetail string                   `json:"problemDetail,omitempty" description:"The underlying error, when there is one to show."`
}

// LadoSSHCheckResponse is the result of reaching the repository over SSH.
type LadoSSHCheckResponse struct {
	OK            bool   `json:"ok"`
	Problem       string `json:"problem,omitempty" description:"Why SSH failed, in English with the details."`
	ProblemCode   string `json:"problemCode,omitempty" enum:"key_home_unknown,key_path_relative,key_missing,key_not_a_file,host_key_untrusted,key_refused,ssh_failed" description:"Why SSH failed, for the app to translate."`
	ProblemDetail string `json:"problemDetail,omitempty" description:"What git or ssh printed, when there is one to show."`
}

// LadoLinkedProjectResponse is the registered project a connection links.
type LadoLinkedProjectResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	Repository string `json:"repository,omitempty" description:"The workspace repository that has the connection's repository. Empty for a single-repo project."`
}

// LadoConnectionCheckResponse is everything a connection needs, checked
// without writing anything.
type LadoConnectionCheckResponse struct {
	Provider   string                     `json:"provider" enum:"github,gitlab"`
	Host       string                     `json:"host" description:"Host the token belongs to."`
	Repository string                     `json:"repository" description:"Repository path on the host."`
	Token      LadoTokenCheckResponse     `json:"token"`
	SSH        LadoSSHCheckResponse       `json:"ssh"`
	Project    *LadoLinkedProjectResponse `json:"project,omitempty" description:"Registered project with the same repository. Connecting links it; without one, connecting clones the repository."`
	Ready      bool                       `json:"ready" description:"Whether connecting can go ahead."`
}

// LadoConnectionResponse is a made connection.
type LadoConnectionResponse struct {
	ProjectID string `json:"projectId"`
	Outcome   string `json:"outcome" enum:"linked,cloned"`
}

// LadoCredentialHostParam names a host in /api/v1/lado/credentials routes.
type LadoCredentialHostParam struct {
	Host string `path:"host" description:"Repository host, such as github.com, gitlab.com or a self-managed GitLab host."`
}

// StoreLadoCredentialRequest stores a host's access token.
type StoreLadoCredentialRequest struct {
	Token string `json:"token" description:"Access token for the host. LADO checks it with the host before storing it."`
}

// LadoCredentialResponse is a host with a stored token.
type LadoCredentialResponse struct {
	Host     string `json:"host"`
	Provider string `json:"provider" enum:"github,gitlab"`
	Login    string `json:"login,omitempty" description:"Account the token belongs to, returned right after storing it."`
}

// LadoCredentialListResponse lists the hosts with a stored token.
type LadoCredentialListResponse struct {
	Credentials []LadoCredentialResponse `json:"credentials"`
}
