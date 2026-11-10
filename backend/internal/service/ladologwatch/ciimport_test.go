package ladologwatch

import (
	"reflect"
	"strings"
	"testing"
)

// The example files use only documentation addresses (RFC 5737) and
// example.test names.

const ciPlain = `
variables:
  PROD1_HOST: 192.0.2.10
  PROD2_HOST: 192.0.2.11

deploy:
  stage: deploy
  script:
    - DOCKER_HOST=tcp://$PROD1_HOST:2375 docker-compose -p grp up -d
    - DOCKER_HOST=tcp://$PROD2_HOST:2375 docker-compose -p grp up -d
`

const ciFull = `
variables:
  PROD1_HOST: 192.0.2.10
  PROD2_HOST:
    value: "192.0.2.11"
    description: second server
  STAGE_HOST: stage.example.test:2376
  BACKUP_HOST: 198.51.100.20
  GROUP: grp

stages: [deploy]

include:
  - local: ci/other.yml

.deploy: &deploy
  stage: deploy
  script:
    - DOCKER_HOST=tcp://$PROD1_HOST:2375 docker-compose -p grp up -d
    - DOCKER_HOST=tcp://${PROD2_HOST}:2375 docker-compose -p grp up -d

deploy_prod:
  <<: *deploy

deploy_stage:
  variables:
    DOCKER_HOST: tcp://$STAGE_HOST
    COMPOSE_PROJECT_NAME: $GROUP-stage
  script:
    - docker compose --project-name=grp-web up -d

deploy_secret:
  extends: .deploy
  script:
    - export DOCKER_HOST=tcp://$SECRET_HOST:2376
    - docker compose -p "$SECRET_GROUP" up -d

deploy_literal:
  script:
    - DOCKER_HOST="tcp://198.51.100.30" docker compose -p grp ps
`

func TestParseGitLabCIPlain(t *testing.T) {
	got := ParseGitLabCI([]byte(ciPlain))
	want := []CIHost{
		{Variable: "PROD1_HOST", DisplayName: "prod1", Endpoint: "tcp://192.0.2.10:2375"},
		{Variable: "PROD2_HOST", DisplayName: "prod2", Endpoint: "tcp://192.0.2.11:2375"},
	}
	if !reflect.DeepEqual(got.Hosts, want) {
		t.Fatalf("hosts = %#v\nwant %#v", got.Hosts, want)
	}
	if !reflect.DeepEqual(got.ComposeProjects, []string{"grp"}) {
		t.Fatalf("compose projects = %q", got.ComposeProjects)
	}
	if len(got.Notes) != 0 {
		t.Fatalf("notes = %q", got.Notes)
	}
}

func TestParseGitLabCIFull(t *testing.T) {
	got := ParseGitLabCI([]byte(ciFull))
	want := []CIHost{
		{Variable: "PROD1_HOST", DisplayName: "prod1", Endpoint: "tcp://192.0.2.10:2375"},
		{Variable: "PROD2_HOST", DisplayName: "prod2", Endpoint: "tcp://192.0.2.11:2375"},
		{Variable: "STAGE_HOST", DisplayName: "stage", Endpoint: "tcp://stage.example.test:2376"},
		{Variable: "BACKUP_HOST", DisplayName: "backup", Endpoint: "tcp://198.51.100.20:2375"},
		{Variable: "SECRET_HOST", DisplayName: "secret", Endpoint: "tcp://$SECRET_HOST:2376", Unresolved: "$SECRET_HOST"},
		{Variable: "DOCKER_HOST", DisplayName: "docker-host", Endpoint: "tcp://198.51.100.30:2375"},
	}
	if !reflect.DeepEqual(got.Hosts, want) {
		t.Fatalf("hosts:\n got %#v\nwant %#v", got.Hosts, want)
	}
	if !reflect.DeepEqual(got.ComposeProjects, []string{"grp", "grp-stage", "grp-web"}) {
		t.Fatalf("compose projects = %q", got.ComposeProjects)
	}
	notes := strings.Join(got.Notes, "\n")
	if !strings.Contains(notes, "include:") || !strings.Contains(notes, "$SECRET_GROUP") {
		t.Fatalf("notes = %q", got.Notes)
	}
}

func TestParseGitLabCIJobVariablesOverride(t *testing.T) {
	got := ParseGitLabCI([]byte(`
variables:
  PORT: "2375"
deploy_a:
  variables:
    APP_HOST: a.example.test
  script:
    - DOCKER_HOST=tcp://$APP_HOST:$PORT docker-compose -p grp up -d
deploy_b:
  variables:
    PORT: "2376"
    APP_HOST: b.example.test
  script:
    - DOCKER_HOST=tcp://$APP_HOST:$PORT docker-compose -p grp up -d
`))
	want := []CIHost{
		{Variable: "APP_HOST", DisplayName: "app", Endpoint: "tcp://a.example.test:2375"},
		{Variable: "APP_HOST", DisplayName: "app", Endpoint: "tcp://b.example.test:2376"},
	}
	if !reflect.DeepEqual(got.Hosts, want) {
		t.Fatalf("hosts:\n got %#v\nwant %#v", got.Hosts, want)
	}
}

func TestParseGitLabCIBrokenYAML(t *testing.T) {
	got := ParseGitLabCI([]byte(`
variables:
  PROD1_HOST: 198.51.100.7
deploy:
  script:
    - DOCKER_HOST=tcp://$PROD1_HOST:2375 docker-compose -p grp up -d
  broken: [unclosed
`))
	want := []CIHost{{Variable: "PROD1_HOST", DisplayName: "prod1", Endpoint: "tcp://198.51.100.7:2375"}}
	if !reflect.DeepEqual(got.Hosts, want) {
		t.Fatalf("hosts = %#v", got.Hosts)
	}
	if !reflect.DeepEqual(got.ComposeProjects, []string{"grp"}) {
		t.Fatalf("compose projects = %q", got.ComposeProjects)
	}
	if len(got.Notes) != 1 || !strings.Contains(got.Notes[0], "YAML") {
		t.Fatalf("notes = %q", got.Notes)
	}
}

func TestParseGitLabCIEdges(t *testing.T) {
	if got := ParseGitLabCI([]byte(strings.Repeat("#", CIMaxBytes+1))); len(got.Hosts) != 0 || len(got.Notes) != 1 {
		t.Fatalf("oversized file = %#v", got)
	}
	for _, data := range []string{"", "just text", "- a\n- b\n", "stages: [build]\n"} {
		if got := ParseGitLabCI([]byte(data)); len(got.Hosts) != 0 || len(got.ComposeProjects) != 0 {
			t.Errorf("%q gave candidates %#v", data, got)
		}
	}
	// A variable that names itself does not loop.
	got := ParseGitLabCI([]byte("variables:\n  LOOP_HOST: $LOOP_HOST\n"))
	if len(got.Hosts) != 1 || got.Hosts[0].Unresolved != "$LOOP_HOST" {
		t.Fatalf("self reference = %#v", got.Hosts)
	}

	// A variable that names itself many times would double on every pass
	// (500 references of 7 bytes: about 1.75 MB, then 875 MB, ...). The
	// expansion stops at its growth bound and leaves the value unresolved,
	// for a *_HOST variable and for a compose project name alike.
	many := strings.Repeat("$A_HOST", 500)
	project := strings.Repeat("${GRP}", 500)
	data := "variables:\n  A_HOST: \"" + many + "\"\n  GRP: \"" + project + "\"\n  COMPOSE_PROJECT_NAME: $GRP\n" +
		"deploy:\n  script:\n    - docker-compose -p $GRP up -d\n"
	got = ParseGitLabCI([]byte(data))
	if len(got.Hosts) != 1 || got.Hosts[0].Unresolved != "$A_HOST" || len(got.Hosts[0].Endpoint) > len(many)+ciMaxExpandGrowth+16 {
		t.Fatalf("doubling host = %d hosts, unresolved %q, endpoint %d bytes", len(got.Hosts), got.Hosts[0].Unresolved, len(got.Hosts[0].Endpoint))
	}
	if len(got.ComposeProjects) != 0 {
		t.Fatalf("doubling compose project = %q", got.ComposeProjects)
	}

	// Within the bound, a value that names another one several times still
	// expands, through more than one pass.
	scope := ciScope{"A": "$B-$B", "B": "$C$C", "C": "x"}
	if v := ciExpand("$A.$A", scope); v != "xx-xx.xx-xx" {
		t.Fatalf("ciExpand = %q", v)
	}
	// One pass that would pass the bound gives the value back as it was.
	big := ciScope{"V": strings.Repeat("y", ciMaxExpandGrowth)}
	if v := ciExpand("$V$V", big); v != "$V$V" {
		t.Fatalf("ciExpand over the bound = %d bytes", len(v))
	}
}

func TestCIEndpoint(t *testing.T) {
	for in, want := range map[string]string{
		"tcp://192.0.2.1":           "tcp://192.0.2.1:2375",
		"tcp://192.0.2.1:2376/":     "tcp://192.0.2.1:2376",
		"tcp://[2001:db8::1]":       "tcp://[2001:db8::1]:2375",
		"tcp://[2001:db8::1]:2376":  "tcp://[2001:db8::1]:2376",
		`"tcp://host.example.test"`: "tcp://host.example.test:2375",
	} {
		if got := ciEndpoint(in); got != want {
			t.Errorf("ciEndpoint(%q) = %q, want %q", in, got, want)
		}
	}
}
