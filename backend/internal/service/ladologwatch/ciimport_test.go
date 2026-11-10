package ladologwatch

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
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

// ciAliasLadder writes levels anchors, each naming the one before it ten
// times: "<name>1: &<name>1 [*<name>0, ...]" (or a "<<:" merge of them when
// merge is set). Without a bound, reading the last one visits 10^levels
// nodes; the whole file is a few kilobytes.
func ciAliasLadder(name, first string, levels int, merge bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s0: &%s0 %s\n", name, name, first)
	for i := 1; i <= levels; i++ {
		refs := strings.TrimSuffix(strings.Repeat(fmt.Sprintf("*%s%d, ", name, i-1), 10), ", ")
		if merge {
			fmt.Fprintf(&b, "%s%d: &%s%d {<<: [%s]}\n", name, i, name, i, refs)
		} else {
			fmt.Fprintf(&b, "%s%d: &%s%d [%s]\n", name, i, name, i, refs)
		}
	}
	return b.String()
}

// TestParseGitLabCIAliasBombs: yaml.v3 leaves aliases unexpanded in a
// yaml.Node, so its alias ratio guard does not cover the walk. Aliases that
// name aliases, nested "<<:" merges and extends that fan out finish within
// the parse's visit budget, keep what was read before it ran out, and say
// that the file was read only in part.
func TestParseGitLabCIAliasBombs(t *testing.T) {
	const partNote = "별칭·병합 키가 너무 많이 펼쳐져"
	hasNote := func(c CICandidates) bool {
		for _, n := range c.Notes {
			if strings.Contains(n, partNote) {
				return true
			}
		}
		return false
	}
	parse := func(t *testing.T, data string) CICandidates {
		t.Helper()
		if len(data) > CIMaxBytes {
			t.Fatalf("test file is %d bytes, over the cap", len(data))
		}
		start := time.Now()
		got := ParseGitLabCI([]byte(data))
		if d := time.Since(start); d > 10*time.Second {
			t.Fatalf("parse took %v", d)
		}
		return got
	}

	t.Run("script aliases are read once per job", func(t *testing.T) {
		// 10^9 scalars through aliases; a job reads each node once, so
		// the host is found and the budget is not reached.
		data := ciAliasLadder("s", `["export DOCKER_HOST=tcp://192.0.2.10:2376"]`, 9, false) +
			"job:\n  script: *s9\n"
		got := parse(t, data)
		if len(got.Hosts) != 1 || got.Hosts[0].Endpoint != "tcp://192.0.2.10:2376" || hasNote(got) {
			t.Fatalf("script alias ladder = %#v", got)
		}
	})

	t.Run("many jobs over one alias ladder", func(t *testing.T) {
		// Each job reads the ladder's few distinct nodes once: the work
		// grows with the file, not with the aliases.
		var b strings.Builder
		b.WriteString(ciAliasLadder("s", `["echo $CI_JOB_ID"]`, 9, false))
		for i := range 3000 {
			fmt.Fprintf(&b, "job%d: {script: *s9}\n", i)
		}
		if got := parse(t, b.String()); hasNote(got) || len(got.Hosts) != 0 {
			t.Fatalf("job fan-out = %#v", got)
		}
	})

	t.Run("merged variables", func(t *testing.T) {
		data := ciAliasLadder("m", `{A_HOST: 192.0.2.11}`, 9, true) +
			"variables:\n  <<: [*m9, *m9]\n"
		got := parse(t, data)
		if !hasNote(got) || len(got.Hosts) != 1 || got.Hosts[0].Endpoint != "tcp://192.0.2.11:2375" {
			t.Fatalf("merge ladder = %#v", got)
		}
	})

	t.Run("key lookup through merges", func(t *testing.T) {
		// The job has no extends or variables key: looking for them goes
		// through every merge.
		data := ciAliasLadder("k", `{script: [echo]}`, 9, true) +
			"job:\n  <<: [*k9, *k9]\n"
		if got := parse(t, data); !hasNote(got) {
			t.Fatalf("lookup ladder notes = %q", got.Notes)
		}
	})

	t.Run("extends fan-out", func(t *testing.T) {
		var b strings.Builder
		b.WriteString(".e0: {variables: {B_HOST: 192.0.2.12}}\n")
		for i := 1; i <= 6; i++ {
			refs := strings.TrimSuffix(strings.Repeat(fmt.Sprintf(".e%d, ", i-1), 30), ", ")
			fmt.Fprintf(&b, ".e%d: {extends: [%s]}\n", i, refs)
		}
		b.WriteString("job: {extends: [.e6, .e6], script: [echo]}\n")
		got := parse(t, b.String())
		if !hasNote(got) || len(got.Hosts) != 1 || got.Hosts[0].Endpoint != "tcp://192.0.2.12:2375" {
			t.Fatalf("extends fan-out = %#v", got)
		}
	})
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
