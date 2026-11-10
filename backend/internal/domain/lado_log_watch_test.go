package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// validLadoLogWatchInput is an input every check passes, with documentation
// addresses only (RFC 5737, example.test).
func validLadoLogWatchInput() domain.LadoLogWatchInput {
	return domain.LadoLogWatchInput{
		Enabled:                true,
		Include:                []string{`(?i)timeout`},
		Exclude:                []string{`healthcheck`},
		HourlyIssueCap:         domain.LadoLogDefaultHourlyIssueCap,
		AutoPlan:               true,
		AutoPlanDailyCap:       domain.LadoLogDefaultAutoPlanDailyCap,
		AutoPlanMinCount:       domain.LadoLogDefaultAutoPlanMinCount,
		DisconnectAlertMinutes: domain.LadoLogDefaultDisconnectAlertMinutes,
		Sources: []domain.LadoLogSourceInput{
			{
				ID: "src-1", Kind: domain.LadoLogSourceKindDockerTCP, DisplayName: "prod1",
				Endpoint: "tcp://192.0.2.10:2375",
				Targets: []domain.LadoLogTargetInput{
					{ID: "tgt-1", Kind: domain.LadoLogTargetComposeProject, Value: "grp", Repository: "github:o/back"},
					{Kind: domain.LadoLogTargetComposeService, Value: "api", ComposeProject: "grp", Service: "grp/api", Repository: strings.Repeat("r", domain.LadoLogMaxRepositoryLength)},
					{Kind: domain.LadoLogTargetName, Value: "worker_1"},
					{Kind: domain.LadoLogTargetLabel, Value: "com.example.team=core:a/b"},
					{Kind: domain.LadoLogTargetLabel, Value: "com.example.watch"},
				},
			},
			{DisplayName: "운영 2", Endpoint: "tcp://docker.example.test:2376", TLS: true},
			{DisplayName: "v6", Endpoint: "tcp://[2001:db8::1]:2376/"},
		},
	}
}

func TestValidateLadoLogWatchInputAcceptsValidInput(t *testing.T) {
	if err := domain.ValidateLadoLogWatchInput(validLadoLogWatchInput()); err != nil {
		t.Fatalf("valid input refused: %v", err)
	}
	if err := domain.ValidateLadoLogWatchInput(domain.LadoLogWatchInput{
		HourlyIssueCap: 1, AutoPlanDailyCap: 1, AutoPlanMinCount: 1, DisconnectAlertMinutes: 1,
	}); err != nil {
		t.Fatalf("input without sources refused: %v", err)
	}
}

// Every refused value comes back as a LadoLogWatchInvalidError naming it.
func TestValidateLadoLogWatchInputRefusals(t *testing.T) {
	endpoint := func(e string) func(*domain.LadoLogWatchInput) {
		return func(in *domain.LadoLogWatchInput) { in.Sources[0].Endpoint = e }
	}
	target := func(i int, kind domain.LadoLogTargetKind, value string) func(*domain.LadoLogWatchInput) {
		return func(in *domain.LadoLogWatchInput) {
			in.Sources[0].Targets[i].Kind = kind
			in.Sources[0].Targets[i].Value = value
			in.Sources[0].Targets[i].ComposeProject = ""
		}
	}
	for _, tc := range []struct {
		name  string
		edit  func(*domain.LadoLogWatchInput)
		field string
	}{
		{"unix socket", endpoint("unix:///var/run/docker.sock"), "sources[0].endpoint"},
		{"named pipe", endpoint("npipe:////./pipe/docker_engine"), "sources[0].endpoint"},
		{"ssh", endpoint("ssh://deploy@192.0.2.10"), "sources[0].endpoint"},
		{"http", endpoint("http://192.0.2.10:2375"), "sources[0].endpoint"},
		{"https", endpoint("https://192.0.2.10:2376"), "sources[0].endpoint"},
		{"user information", endpoint("tcp://user:pw@192.0.2.10:2375"), "sources[0].endpoint"},
		{"user only", endpoint("tcp://user@192.0.2.10:2375"), "sources[0].endpoint"},
		{"no port", endpoint("tcp://192.0.2.10"), "sources[0].endpoint"},
		{"empty port", endpoint("tcp://192.0.2.10:"), "sources[0].endpoint"},
		{"port 0", endpoint("tcp://192.0.2.10:0"), "sources[0].endpoint"},
		{"port 70000", endpoint("tcp://192.0.2.10:70000"), "sources[0].endpoint"},
		{"path", endpoint("tcp://192.0.2.10:2375/v1.43"), "sources[0].endpoint"},
		{"query", endpoint("tcp://192.0.2.10:2375?x=1"), "sources[0].endpoint"},
		{"empty query", endpoint("tcp://192.0.2.10:2375?"), "sources[0].endpoint"},
		{"fragment", endpoint("tcp://192.0.2.10:2375#x"), "sources[0].endpoint"},
		{"percent encoding", endpoint("tcp://docker%2eexample.test:2375"), "sources[0].endpoint"},
		{"dash host", endpoint("tcp://-oProxyCommand:2375"), "sources[0].endpoint"},
		{"no scheme", endpoint("192.0.2.10:2375"), "sources[0].endpoint"},
		{"opaque", endpoint("tcp:192.0.2.10:2375"), "sources[0].endpoint"},
		{"empty", endpoint(""), "sources[0].endpoint"},
		{"bare IPv6", endpoint("tcp://2001:db8::1:2376"), "sources[0].endpoint"},
		{"IPv4 in brackets", endpoint("tcp://[192.0.2.10]:2376"), "sources[0].endpoint"},
		{"underscore host", endpoint("tcp://bad_host.example.test:2376"), "sources[0].endpoint"},
		{"space", endpoint("tcp://192.0.2.10:2375 "), "sources[0].endpoint"},
		{"duplicate endpoint", func(in *domain.LadoLogWatchInput) { in.Sources[1].Endpoint = "TCP://192.0.2.10:2375" }, "sources[1].endpoint"},
		{"unknown kind", func(in *domain.LadoLogWatchInput) { in.Sources[0].Kind = "ssh" }, "sources[0].kind"},
		{"empty display name", func(in *domain.LadoLogWatchInput) { in.Sources[0].DisplayName = "" }, "sources[0].displayName"},
		{"display name with a newline", func(in *domain.LadoLogWatchInput) { in.Sources[0].DisplayName = "a\nb" }, "sources[0].displayName"},
		{"display name with a backtick", func(in *domain.LadoLogWatchInput) { in.Sources[0].DisplayName = "a`b" }, "sources[0].displayName"},
		{"duplicate display name", func(in *domain.LadoLogWatchInput) { in.Sources[1].DisplayName = "prod1" }, "sources[1].displayName"},
		{"duplicate source id", func(in *domain.LadoLogWatchInput) { in.Sources[1].ID = "src-1" }, "sources[1].id"},
		{"bad source id", func(in *domain.LadoLogWatchInput) { in.Sources[1].ID = "../x" }, "sources[1].id"},
		{"duplicate target id", func(in *domain.LadoLogWatchInput) { in.Sources[0].Targets[1].ID = "tgt-1" }, "sources[0].targets[1].id"},
		{"compose project with a space", target(0, domain.LadoLogTargetComposeProject, "my grp"), "sources[0].targets[0].value"},
		{"compose project with a slash", target(0, domain.LadoLogTargetComposeProject, "a/b"), "sources[0].targets[0].value"},
		{"compose service with a quote", target(0, domain.LadoLogTargetComposeService, `api"`), "sources[0].targets[0].value"},
		{"container name regex", target(0, domain.LadoLogTargetName, "api.*"), "sources[0].targets[0].value"},
		{"container name with a leading slash", target(0, domain.LadoLogTargetName, "/api"), "sources[0].targets[0].value"},
		{"empty value", target(0, domain.LadoLogTargetName, ""), "sources[0].targets[0].value"},
		{"label with a space", target(0, domain.LadoLogTargetLabel, "a b=c"), "sources[0].targets[0].value"},
		{"label with two equals", target(0, domain.LadoLogTargetLabel, "a=b=c"), "sources[0].targets[0].value"},
		{"label with a brace", target(0, domain.LadoLogTargetLabel, `a={"x":1}`), "sources[0].targets[0].value"},
		{"unknown target kind", target(0, "regex", "api"), "sources[0].targets[0].kind"},
		{"compose project on a name target", func(in *domain.LadoLogWatchInput) {
			in.Sources[0].Targets[2].ComposeProject = "grp"
		}, "sources[0].targets[2].composeProject"},
		{"bad compose project", func(in *domain.LadoLogWatchInput) {
			in.Sources[0].Targets[1].ComposeProject = "g r p"
		}, "sources[0].targets[1].composeProject"},
		{"bad service name", func(in *domain.LadoLogWatchInput) { in.Sources[0].Targets[0].Service = "a b" }, "sources[0].targets[0].service"},
		{"include that does not compile", func(in *domain.LadoLogWatchInput) { in.Include = []string{"(unclosed"} }, "include[0]"},
		{"exclude that does not compile", func(in *domain.LadoLogWatchInput) { in.Exclude = []string{"ok", `\p{Nope}`} }, "exclude[1]"},
		{"empty pattern", func(in *domain.LadoLogWatchInput) { in.Include = []string{""} }, "include[0]"},
		{"long pattern", func(in *domain.LadoLogWatchInput) { in.Include = []string{strings.Repeat("a", 501)} }, "include[0]"},
		{"too many patterns", func(in *domain.LadoLogWatchInput) { in.Exclude = make([]string, 21) }, "exclude"},
		{"hourly cap 0", func(in *domain.LadoLogWatchInput) { in.HourlyIssueCap = 0 }, "hourlyIssueCap"},
		{"hourly cap 51", func(in *domain.LadoLogWatchInput) { in.HourlyIssueCap = 51 }, "hourlyIssueCap"},
		{"daily plan cap 0", func(in *domain.LadoLogWatchInput) { in.AutoPlanDailyCap = 0 }, "autoPlanDailyCap"},
		{"min count 0", func(in *domain.LadoLogWatchInput) { in.AutoPlanMinCount = 0 }, "autoPlanMinCount"},
		{"disconnect minutes 0", func(in *domain.LadoLogWatchInput) { in.DisconnectAlertMinutes = 0 }, "disconnectAlertMinutes"},
		{"disconnect minutes over a day", func(in *domain.LadoLogWatchInput) { in.DisconnectAlertMinutes = 1441 }, "disconnectAlertMinutes"},
		{"too many sources", func(in *domain.LadoLogWatchInput) {
			for len(in.Sources) <= domain.LadoLogMaxSources {
				in.Sources = append(in.Sources, domain.LadoLogSourceInput{})
			}
		}, "sources"},
		{"too many targets", func(in *domain.LadoLogWatchInput) {
			for len(in.Sources[0].Targets) <= domain.LadoLogMaxTargetsPerSource {
				in.Sources[0].Targets = append(in.Sources[0].Targets, domain.LadoLogTargetInput{Kind: domain.LadoLogTargetName, Value: "x"})
			}
		}, "sources[0].targets"},
		{"repository with a newline", func(in *domain.LadoLogWatchInput) { in.Repository = "github:o/r\n" }, "repository"},
		{"long repository", func(in *domain.LadoLogWatchInput) {
			in.Repository = strings.Repeat("r", domain.LadoLogMaxRepositoryLength+1)
		}, "repository"},
		{"target repository of 513 bytes", func(in *domain.LadoLogWatchInput) {
			in.Sources[0].Targets[2].Repository = strings.Repeat("r", domain.LadoLogMaxRepositoryLength+1)
		}, "sources[0].targets[2].repository"},
		{"target repository with a newline", func(in *domain.LadoLogWatchInput) {
			in.Sources[0].Targets[3].Repository = "github:o/front\n"
		}, "sources[0].targets[3].repository"},
		{"target repository with a NUL", func(in *domain.LadoLogWatchInput) {
			in.Sources[0].Targets[0].Repository = "github:o/\x00back"
		}, "sources[0].targets[0].repository"},
		{"target repository with a tab", func(in *domain.LadoLogWatchInput) {
			in.Sources[0].Targets[4].Repository = "\tgithub:o/back"
		}, "sources[0].targets[4].repository"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := validLadoLogWatchInput()
			tc.edit(&in)
			err := domain.ValidateLadoLogWatchInput(in)
			var invalid *domain.LadoLogWatchInvalidError
			if !errors.As(err, &invalid) {
				t.Fatalf("error = %v, want a LadoLogWatchInvalidError", err)
			}
			if invalid.Field != tc.field || invalid.Message == "" {
				t.Fatalf("field = %q (%q), want %q", invalid.Field, invalid.Message, tc.field)
			}
		})
	}
}

func TestParseLadoLogEndpoint(t *testing.T) {
	for _, tc := range []struct {
		endpoint string
		host     string
		port     int
	}{
		{"tcp://192.0.2.10:2375", "192.0.2.10", 2375},
		{"tcp://docker.example.test:2376/", "docker.example.test", 2376},
		{"TCP://[2001:db8::1]:2376", "2001:db8::1", 2376},
		{"tcp://h:1", "h", 1},
		{"tcp://h:65535", "h", 65535},
	} {
		host, port, err := domain.ParseLadoLogEndpoint(tc.endpoint)
		if err != nil || host != tc.host || port != tc.port {
			t.Errorf("ParseLadoLogEndpoint(%q) = %q %d %v, want %q %d", tc.endpoint, host, port, err, tc.host, tc.port)
		}
	}
	_, _, err := domain.ParseLadoLogEndpoint("unix:///var/run/docker.sock")
	if err == nil || !strings.Contains(err.Error(), "tcp://host:port 만") {
		t.Fatalf("unix endpoint error = %v, want the tcp-only message", err)
	}
}

// The notices follow the issue's wording and keep log text out of the line.
func TestLadoLogWatchNotices(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{domain.LadoNoticeLogIncident(12, "java.lang.NullPointerException", 3, domain.LadoLogPlanRequested),
			"LADO 알림: 서버 오류 감지 — #12 (java.lang.NullPointerException, 3회, 계획 요청됨)"},
		{domain.LadoNoticeLogIncident(12, "Boom`\n[x](http://evil.example.test)", 1, domain.LadoLogPlanHeldCap),
			"LADO 알림: 서버 오류 감지 — #12 (Boomxhttp://evil.example.test, 1회, 보류(상한))"},
		{domain.LadoNoticeLogIncident(3, "", 1, domain.LadoLogPlanHeldMinCount(2)),
			"LADO 알림: 서버 오류 감지 — #3 (오류, 1회, 보류(2회 미만))"},
		{domain.LadoNoticeLogRecurred(14, 12, "ValueError", domain.LadoLogPlanOff),
			"LADO 알림: 서버 오류 감지 — #14 (ValueError, 이전 #12, 자동 계획 꺼짐)"},
		{domain.LadoNoticeLogBurst(5), "LADO 알림: 오류 폭주 — 시간당 이슈 상한 5개에 닿아 이슈 없이 기록합니다"},
		{domain.LadoNoticeLogSourceDown("운영`1\n", 12), "LADO 알림: 로그 출처 끊김 — 운영 1, 12분"},
	} {
		if tc.got != tc.want {
			t.Errorf("notice = %q\nwant     %q", tc.got, tc.want)
		}
	}
	long := domain.LadoNoticeLogIncident(1, strings.Repeat("A", 300), 1, domain.LadoLogPlanRequested)
	if !strings.Contains(long, "("+strings.Repeat("A", 120)+", 1회") {
		t.Fatalf("long kind = %q, want it cut to 120 characters", long)
	}
}

func TestDefaultLadoLogWatchSettings(t *testing.T) {
	s := domain.DefaultLadoLogWatchSettings("p")
	if s.ProjectID != "p" || s.Enabled || s.HourlyIssueCap != 5 || !s.AutoPlan || s.AutoPlanDailyCap != 5 ||
		s.AutoPlanMinCount != 1 || s.DisconnectAlertMinutes != 10 || s.Sources == nil {
		t.Fatalf("defaults = %+v", s)
	}
}
