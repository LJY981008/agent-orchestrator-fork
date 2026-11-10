// Package ladologwatch watches the container logs of a project's production
// servers and turns errors into LADO work (issue #87, ADR 0012).
//
// The daemon follows container logs through the Docker Engine API without an
// LLM. Each log line goes through a Detector, which gathers a stack trace or
// an error line into one Incident and gives it a service-level fingerprint:
// sha256 of the service name, the exception kind and the first application
// frame, normalized so that numbers, UUIDs, hex ids and times do not matter.
// Hosts, container ids and container names are not part of the fingerprint,
// so the same error from two replicas of one service, on one server or on
// two, is one fingerprint.
//
// A new fingerprint becomes a tracker issue and a LADO task whose source is
// "log". Before anything leaves the daemon, Redact masks secrets and hosts,
// and IssueText puts every piece of log text under an "untrusted production
// log excerpt" heading, inside a code fence longer than any backtick run in
// it, so that text an attacker wrote into a log reads as data, not as an
// instruction to the plan worker.
//
// ParseGitLabCI reads a project's .gitlab-ci.yml for the Docker hosts and
// compose projects its deploy jobs use, as candidates the user confirms.
//
// The package's files share one namespace. Unexported helpers carry the
// prefix of the file group that owns them: detect, redact, issue and ci for
// the pure logic, docker, certs and source for the Engine API client, logs
// and follow for the log streams, incident for the occurrence handler.
package ladologwatch
