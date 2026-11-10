package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestLadoWatchDefaults(t *testing.T) {
	d := LadoWatchDefaults()
	want := LadoWatchSettings{
		CompactWindow: 200_000, TaskCostCents: 100_00, SessionCostCents: 30_00,
		SessionCompactions: 3, StallMinutes: 120, WakeEnabled: true,
	}
	if d != want {
		t.Fatalf("defaults = %+v, want %+v", d, want)
	}
	if d.Stall() != 2*time.Hour {
		t.Fatalf("default stall = %v, want 2h", d.Stall())
	}
	if got := LadoWatchEffective(LadoWatchOverrides{}, LadoWatchOverrides{}); got != want {
		t.Fatalf("effective without rows = %+v, want the defaults", got)
	}
}

func TestLadoWatchEffectiveLayersProjectOverGlobal(t *testing.T) {
	intp := func(v int) *int { return &v }
	centsp := func(v int64) *int64 { return &v }
	boolp := func(v bool) *bool { return &v }
	global := LadoWatchOverrides{CompactWindow: intp(300_000), TaskCostCents: centsp(50_00), WakeEnabled: boolp(false)}
	project := LadoWatchOverrides{CompactWindow: intp(150_000), SessionCompactions: intp(5)}
	got := LadoWatchEffective(global, project)
	want := LadoWatchSettings{
		CompactWindow: 150_000, TaskCostCents: 50_00, SessionCostCents: LadoDefaultSessionCostCents,
		SessionCompactions: 5, StallMinutes: LadoDefaultStallMinutes, WakeEnabled: false,
	}
	if got != want {
		t.Fatalf("effective = %+v, want %+v", got, want)
	}
	// The project turns waking back on over the global row.
	if got := LadoWatchEffective(global, LadoWatchOverrides{WakeEnabled: boolp(true)}); !got.WakeEnabled || got.CompactWindow != 300_000 {
		t.Fatalf("effective with the project waking = %+v", got)
	}
	// A value out of range falls back to the row below; the row's other
	// values still apply.
	bad := LadoWatchOverrides{CompactWindow: intp(50_000), StallMinutes: intp(30)}
	if got := LadoWatchEffective(global, bad); got.CompactWindow != 300_000 || got.StallMinutes != 30 {
		t.Fatalf("effective with a bad project value = %+v", got)
	}
}

func TestLadoWatchOverridesValidate(t *testing.T) {
	intp := func(v int) *int { return &v }
	centsp := func(v int64) *int64 { return &v }
	for name, tc := range map[string]struct {
		o  LadoWatchOverrides
		ok bool
	}{
		"nothing set":                    {LadoWatchOverrides{}, true},
		"the lowest compact window":      {LadoWatchOverrides{CompactWindow: intp(100_000)}, true},
		"the highest compact window":     {LadoWatchOverrides{CompactWindow: intp(1_000_000)}, true},
		"a compact window below 100000":  {LadoWatchOverrides{CompactWindow: intp(99_999)}, false},
		"a compact window above 1000000": {LadoWatchOverrides{CompactWindow: intp(1_000_001)}, false},
		"a one cent task cost":           {LadoWatchOverrides{TaskCostCents: centsp(1)}, true},
		"a zero task cost":               {LadoWatchOverrides{TaskCostCents: centsp(0)}, false},
		"a session cost too high":        {LadoWatchOverrides{SessionCostCents: centsp(LadoMaxCostCents + 1)}, false},
		"zero compactions":               {LadoWatchOverrides{SessionCompactions: intp(0)}, false},
		"101 compactions":                {LadoWatchOverrides{SessionCompactions: intp(101)}, false},
		"a nine minute stall":            {LadoWatchOverrides{StallMinutes: intp(9)}, false},
		"a week's stall":                 {LadoWatchOverrides{StallMinutes: intp(7 * 24 * 60)}, true},
	} {
		err := tc.o.Validate()
		if tc.ok && err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !tc.ok && !errors.Is(err, ErrLadoWatchSettingOutOfRange) {
			t.Fatalf("%s: err = %v, want out of range", name, err)
		}
	}
}

func TestLadoWakeTextsAndLimits(t *testing.T) {
	if LadoWakeLimitResumeText != "사용 한도가 풀렸습니다. 멈춘 곳부터 이어서 진행하세요." {
		t.Fatalf("resume text = %q", LadoWakeLimitResumeText)
	}
	if LadoWakeStallNudgeText != "지금 상태를 보고하거나 이어서 진행하세요." {
		t.Fatalf("nudge text = %q", LadoWakeStallNudgeText)
	}
	if LadoWakeMaxResumes != 3 || LadoWakeMaxNudges != 1 || LadoWakeResumeDelay != 5*time.Minute || LadoLimitGuessedReset != time.Hour {
		t.Fatal("wake limits changed")
	}
	if LadoWaitingAccountLimited != "account_limited" || LadoWaitingAgentUnavailable != "agent_unavailable" {
		t.Fatal("waiting reasons changed")
	}
}

func TestLadoAccountKey(t *testing.T) {
	for _, tc := range []struct {
		harness AgentHarness
		account string
		want    string
		ok      bool
	}{
		{HarnessClaudeCode, "", "claude:device-claude", true},
		{HarnessClaudeCode, "work", "claude:work", true},
		{HarnessCodex, "", "codex:device-codex", true},
		{HarnessCodex, "acct-2", "codex:acct-2", true},
		{"opencode", "x", "", false},
	} {
		got, ok := LadoAccountKey(tc.harness, tc.account)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("LadoAccountKey(%s, %q) = %q, %v; want %q, %v", tc.harness, tc.account, got, ok, tc.want, tc.ok)
		}
	}
}

func TestLadoAccountLimitActive(t *testing.T) {
	reset := time.Date(2026, 10, 29, 11, 20, 0, 0, time.UTC)
	l := LadoAccountLimit{ResetAt: reset}
	if !l.Active(reset.Add(-time.Second)) || l.Active(reset) || l.Active(reset.Add(time.Minute)) {
		t.Fatal("a limit holds until its reset and no longer")
	}
	if l.Released() || l.ReleasedEarly() || l.SparesSessionsBefore() {
		t.Fatal("a live limit reads as released")
	}
	// A released limit no longer holds, even before its reset.
	for _, tc := range []struct {
		reason       LadoLimitReleaseReason
		early, spare bool
	}{
		{LadoLimitReleaseReset, false, false},
		{LadoLimitReleaseTurnCompleted, true, false},
		{LadoLimitReleaseAccountChanged, true, true},
		{LadoLimitReleaseManual, true, true},
	} {
		r := LadoAccountLimit{ResetAt: reset, ReleasedAt: reset.Add(-time.Hour), ReleaseReason: tc.reason}
		if r.Active(reset.Add(-2*time.Hour)) || !r.Released() {
			t.Fatalf("%s: a released limit still holds", tc.reason)
		}
		if r.ReleasedEarly() != tc.early || r.SparesSessionsBefore() != tc.spare {
			t.Fatalf("%s: early %v spare %v, want %v %v", tc.reason, r.ReleasedEarly(), r.SparesSessionsBefore(), tc.early, tc.spare)
		}
	}
}

func TestLadoLimitReleaseReasons(t *testing.T) {
	for _, r := range []LadoLimitReleaseReason{"reset", "account_changed", "manual", "turn_completed"} {
		if !r.Valid() {
			t.Fatalf("%q is not valid", r)
		}
	}
	for _, r := range []LadoLimitReleaseReason{LadoLimitReleaseNone, "expired", "Manual"} {
		if r.Valid() {
			t.Fatalf("%q is valid", r)
		}
	}
	if errors.Is(ErrLadoAccountLimitNotFound, ErrLadoAccountLimitNotActive) {
		t.Fatal("the two release errors are the same")
	}
}

func TestParseLadoAccountKey(t *testing.T) {
	for _, tc := range []struct {
		key      string
		provider AgentAccountProvider
		id       string
		ok       bool
	}{
		{"claude:device-claude", AgentAccountProviderClaude, "device-claude", true},
		{"codex:acct_2.x", AgentAccountProviderCodex, "acct_2.x", true},
		{"claude:", "", "", false},
		{"opencode:x", "", "", false},
		{"Claude:x", "", "", false},
		{"claude:a/b", "", "", false},
		{"claude:a:b", "", "", false},
		{"claude:a b", "", "", false},
		{"claude:x\n", "", "", false},
		{"", "", "", false},
	} {
		provider, id, ok := ParseLadoAccountKey(tc.key)
		if provider != tc.provider || id != tc.id || ok != tc.ok {
			t.Fatalf("ParseLadoAccountKey(%q) = %q %q %v; want %q %q %v", tc.key, provider, id, ok, tc.provider, tc.id, tc.ok)
		}
	}
	// Every key LadoAccountKey makes parses back.
	for _, h := range []AgentHarness{HarnessClaudeCode, HarnessCodex} {
		for _, account := range []string{"", "work"} {
			key, _ := LadoAccountKey(h, account)
			if _, _, ok := ParseLadoAccountKey(key); !ok {
				t.Fatalf("%q does not parse", key)
			}
		}
	}
}

func TestLadoNoticeAccountLines(t *testing.T) {
	seoul := time.FixedZone("KST", 9*60*60)
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, seoul)
	reset := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC) // 19:00 KST
	for _, tc := range []struct {
		got, want string
	}{
		{LadoNoticeAccountLimited("claude:device-claude", reset, false, now, seoul), "LADO 알림: Claude 계정 한도 — 새 워커 대기, 19:00 리셋"},
		{LadoNoticeAccountLimited("codex:work", reset, true, now, seoul), "LADO 알림: Codex 계정(work) 한도 — 새 워커 대기, 19:00 리셋 추정"},
		{LadoNoticeAccountLimited("claude:device-claude", reset.Add(12*time.Hour), false, now, seoul), "LADO 알림: Claude 계정 한도 — 새 워커 대기, 11/11 07:00 리셋"},
		{LadoNoticeAccountReleased("claude:device-claude", LadoLimitReleaseAccountChanged, 3), "LADO 알림: Claude 계정 한도 해제(계정 바뀜) — 계정 한도 대기 3건 다시 확인"},
		{LadoNoticeAccountReleased("claude:sub", LadoLimitReleaseManual, 0), "LADO 알림: Claude 계정(sub) 한도 해제(수동 해제) — 계정 한도 대기 0건 다시 확인"},
		{LadoNoticeAccountReleased("codex:device-codex", LadoLimitReleaseReset, 1), "LADO 알림: Codex 계정 한도 해제(리셋) — 계정 한도 대기 1건 다시 확인"},
		{LadoNoticeAccountReleased("codex:device-codex", LadoLimitReleaseTurnCompleted, 2), "LADO 알림: Codex 계정 한도 해제(작업 재개 확인) — 계정 한도 대기 2건 다시 확인"},
		{LadoNoticeAccountReleased("codex:device-codex", "other", 2), "LADO 알림: Codex 계정 한도 해제(알 수 없는 이유) — 계정 한도 대기 2건 다시 확인"},
	} {
		if tc.got != tc.want {
			t.Fatalf("notice = %q, want %q", tc.got, tc.want)
		}
	}
	// A nil zone is time.Local.
	if got, want := LadoNoticeAccountLimited("claude:device-claude", now, false, now, nil), "LADO 알림: Claude 계정 한도 — 새 워커 대기, "+now.In(time.Local).Format("15:04")+" 리셋"; got != want {
		t.Fatalf("notice in time.Local = %q, want %q", got, want)
	}
	// The lines never carry more than the key.
	if line := LadoNoticeAccountLimited("claude:"+strings.Repeat("a", 300), reset, false, now, seoul); len([]rune(line)) > LadoNoticeMaxRunes {
		t.Fatalf("a long account makes a %d-rune line", len([]rune(line)))
	}
}

func TestLadoAlertLines(t *testing.T) {
	for _, tc := range []struct {
		alert LadoAlert
		want  string
	}{
		{LadoAlert{Kind: LadoAlertTaskCost, Level: 1, Value: 123_45, Threshold: 100_00},
			"LADO 알림: #42 작업 비용 추정 $123.45, 기준 $100.00 넘음"},
		{LadoAlert{Kind: LadoAlertTaskCost, Level: 2, Value: 201_00, Threshold: 100_00},
			"LADO 알림: #42 작업 비용 추정 $201.00, 기준 $100.00 의 2배 넘음"},
		{LadoAlert{Kind: LadoAlertSessionCost, SessionID: "mer-7", Level: 1, Value: 31_00, Threshold: 30_00},
			"LADO 알림: #42 세션 mer-7 비용 추정 $31.00, 기준 $30.00 넘음"},
		{LadoAlert{Kind: LadoAlertSessionCost, SessionID: "mer-7", Level: 2, Value: 61_00, Threshold: 30_00},
			"LADO 알림: #42 세션 mer-7 비용 추정 $61.00, 기준 $30.00 의 2배 넘음"},
		{LadoAlert{Kind: LadoAlertSessionCompactions, SessionID: "mer-7", Level: 1, Value: 3, Threshold: 3},
			"LADO 알림: #42 세션 mer-7 compact 3번(기준 3번) — 파트를 다시 쪼갤지 확인하세요"},
		{LadoAlert{Kind: LadoAlertLimited, SessionID: "mer-7", Level: 1},
			"LADO 알림: #42 세션 mer-7 사용 한도로 멈춤"},
		{LadoAlert{Kind: LadoAlertLimitResumed, SessionID: "mer-7", Level: 2, Value: 2, Threshold: 3},
			"LADO 알림: #42 세션 mer-7 에 한도 재개 문구를 보냄(2/3)"},
		{LadoAlert{Kind: LadoAlertStallNudged, SessionID: "mer-7", Level: 1, Value: 120},
			"LADO 알림: #42 세션 mer-7 가 120분 넘게 멈춰 재촉 문구를 보냄"},
		{LadoAlert{Kind: LadoAlertStalled, SessionID: "mer-7", Level: 1, Value: 240},
			"LADO 알림: #42 세션 mer-7 가 240분 넘게 멈춤"},
		{LadoAlert{Kind: LadoAlertAgentUnavailable, Level: 1},
			"LADO 알림: #42 에이전트 실행 파일을 쓸 수 없어 워커를 기다리게 함"},
	} {
		if got := tc.alert.Line(42); got != tc.want {
			t.Fatalf("%s level %d line = %q, want %q", tc.alert.Kind, tc.alert.Level, got, tc.want)
		}
		if !tc.alert.Kind.Valid() {
			t.Fatalf("%s is not valid", tc.alert.Kind)
		}
	}
	if LadoAlertKind("weather").Valid() || LadoAlertKind("").Valid() {
		t.Fatal("an unknown kind is valid")
	}
	if got := (LadoAlert{Kind: "weather", SessionID: "s"}).Line(1); !strings.HasPrefix(got, LadoNoticePrefix) {
		t.Fatalf("unknown kind line = %q", got)
	}
	long := LadoAlert{Kind: LadoAlertSessionCompactions, SessionID: SessionID(strings.Repeat("s", 300)), Value: 3, Threshold: 3}
	if got := []rune(long.Line(1)); len(got) != LadoNoticeMaxRunes {
		t.Fatalf("a long line has %d runes, want it cut to %d", len(got), LadoNoticeMaxRunes)
	}
}

func TestLadoAlertNotificationIDAndSeen(t *testing.T) {
	a := LadoAlert{ID: 17}
	if a.NotificationID() != "lado-alert:17" || a.Seen() {
		t.Fatalf("alert = %q seen %v", a.NotificationID(), a.Seen())
	}
	a.SeenAt = time.Now()
	if !a.Seen() {
		t.Fatal("a seen alert is not seen")
	}
}

func TestLadoCents(t *testing.T) {
	for cents, want := range map[int64]string{0: "$0.00", 5: "$0.05", 100_00: "$100.00", 123_45: "$123.45", -150: "-$1.50"} {
		if got := LadoCents(cents); got != want {
			t.Fatalf("LadoCents(%d) = %q, want %q", cents, got, want)
		}
	}
}

func TestLadoPlanWorkerSessionRole(t *testing.T) {
	if (LadoPlanWorkerSession{Role: LadoPlanRoleDesigner}).SessionRole() != LadoSessionDesigner ||
		(LadoPlanWorkerSession{Role: LadoPlanRoleCritic}).SessionRole() != LadoSessionCritic {
		t.Fatal("plan worker roles do not map to the timings roles")
	}
}
