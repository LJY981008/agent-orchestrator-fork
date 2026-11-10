package ladologwatch

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		secret []string
		keep   []string
	}{
		{
			name:   "authorization header",
			in:     "GET /orders Authorization: Bearer abcdefghijklmnop12345",
			secret: []string{"abcdefghijklmnop12345"},
			keep:   []string{"GET /orders", "Authorization: [REDACTED]"},
		},
		{
			name:   "authorization in json",
			in:     `{"headers":{"authorization":"Basic dXNlcjpwYXNzd29yZA==","x":"1"}}`,
			secret: []string{"dXNlcjpwYXNzd29yZA=="},
			keep:   []string{`"x":"1"`},
		},
		{
			name:   "bearer",
			in:     "upstream said bearer eyAbCdEf0123456789xyz",
			secret: []string{"eyAbCdEf0123456789xyz"},
		},
		{
			name:   "jwt",
			in:     "token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.c2lnbmF0dXJlLXZhbHVl rejected",
			secret: []string{"eyJhbGciOiJIUzI1NiJ9", "c2lnbmF0dXJlLXZhbHVl"},
			keep:   []string{"rejected"},
		},
		{
			name: "tracker tokens",
			// The GitLab and GitHub tokens are put together here so that the secret
			// scanner does not take the test for a leak.
			in:     "clone with " + "glpat-" + "AbCdEf0123456789xyzw and " + "ghp_" + strings.Repeat("Ab0", 12),
			secret: []string{"AbCdEf0123456789xyzw", "ghp_Ab0"},
			keep:   []string{"clone with"},
		},
		{
			name:   "password key values",
			in:     `login failed password=hunter2xyz&user=bob db_password: "s3cr3t value" api_key='k-123456'`,
			secret: []string{"hunter2xyz", "s3cr3t value", "k-123456"},
			keep:   []string{"user=bob", "login failed"},
		},
		{
			name:   "cookie",
			in:     "Cookie: SESSION=abc123def456; theme=dark",
			secret: []string{"abc123def456", "theme=dark"},
		},
		{
			name:   "set-cookie",
			in:     "Set-Cookie: sid=zzzzzzzz9999; HttpOnly",
			secret: []string{"zzzzzzzz9999"},
		},
		{
			name:   "email",
			in:     "mail to someone.name+tag@example.test bounced",
			secret: []string{"someone.name", "example.test"},
			keep:   []string{"mail to", "bounced", "[EMAIL]"},
		},
		{
			name:   "phone",
			in:     "sms to 010-1234-5678 and +82 10-9876-5432 and 01055556666 failed",
			secret: []string{"1234-5678", "9876-5432", "01055556666"},
			keep:   []string{"failed"},
		},
		{
			name:   "card numbers",
			in:     "charge 4111 1111 1111 1111 and 5500-0000-0000-0004 ok, order 1234567890123 kept",
			secret: []string{"4111 1111", "5500-0000"},
			keep:   []string{"order 1234567890123 kept", "[CARD]"},
		},
		{
			name:   "connection string user",
			in:     "dial postgres://app:pa55w0rd@db.example.test:5432/orders failed; redis app:pw@cache.example.test",
			secret: []string{"pa55w0rd", "app:", "db.example.test", "cache.example.test", "pw@"},
			keep:   []string{"postgres://", ":5432/orders"},
		},
		{
			name:   "ipv4",
			in:     "connect 192.0.2.17:2375 from 198.51.100.4 refused",
			secret: []string{"192.0.2.17", "198.51.100.4"},
			keep:   []string{"[IP]:2375", "refused"},
		},
		{
			name:   "ipv6",
			in:     "peer [2001:db8::1]:443 and fe80::1 gone at 09:00:00, self::create",
			secret: []string{"2001:db8::1", "fe80::1"},
			keep:   []string{"09:00:00", "self::create"},
		},
		{
			name:   "url host",
			in:     "GET https://internal.example.test/api/v1 503",
			secret: []string{"internal.example.test"},
			keep:   []string{"https://[HOST]/api/v1 503"},
		},
		{
			name:   "configured hosts",
			in:     "docker1.example.test:2376 and registry.example.test/grp/api:1.2 and DOCKER1.EXAMPLE.TEST",
			secret: []string{"docker1.example.test", "registry.example.test", "DOCKER1"},
			keep:   []string{"[HOST]:2376", "[HOST]/grp/api:1.2"},
		},
		{
			name: "stack frames stay",
			in:   "\tat com.example.app.OrderService.close(OrderService.java:57)\n    at closeOrder (/app/src/routes/orders.js:27:19)",
			keep: []string{"com.example.app.OrderService.close(OrderService.java:57)", "/app/src/routes/orders.js:27:19"},
		},
	}
	hosts := []string{"docker1.example.test", "registry.example.test"}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(tc.in, hosts)
			for _, s := range tc.secret {
				if strings.Contains(got, s) {
					t.Errorf("%q left in %q", s, got)
				}
			}
			for _, s := range tc.keep {
				if !strings.Contains(got, s) {
					t.Errorf("%q missing from %q", s, got)
				}
			}
		})
	}
}

func TestRedactLuhn(t *testing.T) {
	for in, want := range map[string]bool{
		"4111111111111111":    true,
		"4111111111111112":    false,
		"378282246310005":     true,
		"123456789012":        false,
		"6011 1111 1111 1117": true,
	} {
		if got := redactLuhn(in); got != want {
			t.Errorf("redactLuhn(%q) = %v, want %v", in, got, want)
		}
	}
}
