package redact

import (
	"strings"
	"testing"
)

// Fixtures are synthetic. Real secrets must never enter a test file.
func TestRedactsCommonSecretShapes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		gone string
		rule string
	}{
		{"github classic", "token is ghp_" + strings.Repeat("a", 36) + " ok", "ghp_", "github_token"},
		{"github fine grained", "github_pat_" + strings.Repeat("b", 30), "github_pat_", "github_fine_grained"},
		{"aws access key", "key AKIAIOSFODNN7EXAMPLE here", "AKIAIOSFODNN7EXAMPLE", "aws_access_key"},
		{"slack", "xoxb-123456789012-abcdefghijkl", "xoxb-", "slack_token"},
		{"google", "AIza" + strings.Repeat("c", 35), "AIza", "google_api_key"},
		{"jwt", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk", "eyJhbGci", "jwt"},
		{"assigned secret", `API_KEY="s3cr3tvalue12345"`, "s3cr3tvalue12345", "assigned_secret"},
		{"bearer", "Authorization: Bearer abcdefghij1234567890", "abcdefghij1234567890", "bearer_token"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Text(tc.in)
			if !got.Redacted {
				t.Fatalf("secret not detected in %q", tc.in)
			}
			if strings.Contains(got.Text, tc.gone) {
				t.Errorf("secret survived redaction: %q", got.Text)
			}
			found := false
			for _, f := range got.Findings {
				if f.Rule == tc.rule {
					found = true
				}
			}
			if !found {
				t.Errorf("expected rule %q, got %v", tc.rule, got.Findings)
			}
		})
	}
}

func TestRedactsPrivateKeyBlocks(t *testing.T) {
	in := "before\n-----BEGIN RSA PRIVATE KEY-----\nMIIEabcdef\nghijkl\n-----END RSA PRIVATE KEY-----\nafter"
	got := Text(in)

	if strings.Contains(got.Text, "MIIEabcdef") {
		t.Error("private key body survived redaction")
	}
	if !strings.Contains(got.Text, "before") || !strings.Contains(got.Text, "after") {
		t.Error("redaction destroyed surrounding context")
	}
}

func TestRedactsConnectionStringPasswordOnly(t *testing.T) {
	// The host and user are useful context for a tutorial; only the password
	// must go.
	got := Text("postgres://appuser:hunter2secret@db.internal:5432/mydb")

	if strings.Contains(got.Text, "hunter2secret") {
		t.Errorf("password survived: %q", got.Text)
	}
	if !strings.Contains(got.Text, "db.internal") {
		t.Errorf("host should be preserved: %q", got.Text)
	}
	if !strings.Contains(got.Text, "appuser") {
		t.Errorf("username should be preserved: %q", got.Text)
	}
}

func TestRedactsAWSAccountIDInARN(t *testing.T) {
	got := Text("arn:aws:iam::123456789012:role/MyRole")
	if strings.Contains(got.Text, "123456789012") {
		t.Errorf("account id survived: %q", got.Text)
	}
	if !strings.Contains(got.Text, "role/MyRole") {
		t.Errorf("resource path should be preserved: %q", got.Text)
	}
}

func TestPlaceholdersAreActionable(t *testing.T) {
	// "[REDACTED]" produces an unusable tutorial. A placeholder should tell
	// the reader what is missing and how to get it.
	got := Text("export GITHUB_TOKEN=ghp_" + strings.Repeat("z", 36))
	if !strings.Contains(got.Text, "ask operator") {
		t.Errorf("placeholder should be actionable, got %q", got.Text)
	}
}

func TestCleanTextIsUnchanged(t *testing.T) {
	// False positives are costly: they corrupt otherwise-good content.
	clean := []string{
		"Run go build -o midden ./cmd/midden and then ./midden doctor",
		"The session was 681 MiB and failed to resume after 18 seconds.",
		"See https://github.com/mekjr1/midden for the source.",
		"func Classify(kind string) Class { return Signal }",
	}
	for _, in := range clean {
		got := Text(in)
		if got.Redacted {
			t.Errorf("false positive on %q: %v", in, got.Findings)
		}
		if got.Text != in {
			t.Errorf("clean text modified:\n got %q\nwant %q", got.Text, in)
		}
	}
}

func TestRedactionIsIdempotent(t *testing.T) {
	// Nuggets are redacted at extraction and again on the way into the store.
	// A second pass must not nest placeholders.
	once := Text("token: ghp_" + strings.Repeat("q", 36))
	twice := Text(once.Text)

	if twice.Text != once.Text {
		t.Errorf("second pass changed the text:\n once %q\ntwice %q", once.Text, twice.Text)
	}
}

func TestSummaryNeverEchoesTheSecret(t *testing.T) {
	secret := "ghp_" + strings.Repeat("w", 36)
	got := Text("key " + secret)
	s := Summary(got.Findings)

	if strings.Contains(s, secret) {
		t.Fatal("summary leaked the secret it was reporting")
	}
	if !strings.Contains(s, "github_token") {
		t.Errorf("summary should name the rule, got %q", s)
	}
}

func TestScanDoesNotModify(t *testing.T) {
	in := "AKIAIOSFODNN7EXAMPLE"
	fs := Scan(in)
	if len(fs) == 0 {
		t.Error("scan should report findings")
	}
	if in != "AKIAIOSFODNN7EXAMPLE" {
		t.Error("scan must not mutate its input")
	}
}
