package ladodbchange

import (
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRedactSecretsAndPasswords(t *testing.T) {
	secrets := map[string]string{"LADO_DB_DSN": "postgres://deploy:p@ss w0rd!@db:5432/app", "SHORT": "abc", "TOKEN_X": "zz-secret-zz"}
	text := strings.Join([]string{
		"connecting to postgres://deploy:p@ss w0rd!@db:5432/app",
		"escaped " + url.QueryEscape("zz-secret-zz/") + " and " + url.QueryEscape(secrets["LADO_DB_DSN"]),
		"psql: error: connection to server at \"db\" failed: FATAL: password authentication failed for user \"deploy\"",
		"other url mysql://root:hunter22@10.0.0.1/db",
		"host=db user=app password=hunter33 dbname=app",
		"PGPASSWORD='quoted pass' psql",
		"Authorization: Bearer abcdefghijklmnop",
		"value abc stays",
	}, "\n")
	got := Redact(text, secrets)
	for _, leaked := range []string{"p@ss w0rd!", "zz-secret-zz", url.QueryEscape("zz-secret-zz"), "hunter22", "hunter33", "quoted pass", "abcdefghijklmnop"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("%q survived:\n%s", leaked, got)
		}
	}
	if !strings.Contains(got, "value abc stays") || !strings.Contains(got, "password authentication failed") {
		t.Fatalf("too much went:\n%s", got)
	}
}

func TestRedactOutputKeepsTheEnd(t *testing.T) {
	secret := "very-secret-value"
	out := strings.Repeat("가", OutputKeepMax) + "\nfailed with " + secret + "\n"
	got := RedactOutput([]byte(out), map[string]string{"S": secret})
	if len(got) > OutputKeepMax || !utf8.Valid(got) || strings.Contains(string(got), secret) || !strings.HasSuffix(string(got), "failed with [REDACTED]\n") {
		t.Fatalf("output = %d bytes, end %q", len(got), got[len(got)-40:])
	}
	if got := RedactOutput([]byte("bad \xff byte"), nil); !utf8.Valid(got) {
		t.Fatalf("invalid UTF-8 kept: %q", got)
	}
}

func TestRedactLine(t *testing.T) {
	line := RedactLine("exit 1:\n  password=abc123  \t"+strings.Repeat("x", 400), nil)
	if strings.Contains(line, "abc123") || strings.ContainsAny(line, "\n\t") || utf8.RuneCountInString(line) > MessageMax+1 || !strings.HasSuffix(line, "…") {
		t.Fatalf("line = %q", line)
	}
}
