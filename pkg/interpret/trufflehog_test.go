package interpret

import (
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestTruffleHog(t *testing.T) {
	// NDJSON: a verified AWS key, an unverified GitHub token, and a non-JSON log line to skip.
	out := strings.Join([]string{
		`{"SourceMetadata":{"Data":{"Filesystem":{"file":"/src/config.env","line":3}}},"DetectorName":"AWS","Verified":true,"Redacted":"AKIA****ABCD"}`,
		`2024/01/01 scanning...`,
		`{"SourceMetadata":{"Data":{"Filesystem":{"file":"/src/app.py","line":10}}},"DetectorName":"GitHub","Verified":false,"Redacted":"ghp_****"}`,
	}, "\n")

	obs, err := TruffleHog([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 2 {
		t.Fatalf("want 2 observations, got %d", len(obs))
	}

	aws := obs[0]
	if aws.Severity != "high" || aws.Location != "/src/config.env:3" || aws.RuleID != "trufflehog:AWS" {
		t.Fatalf("verified AWS observation wrong: %+v", aws)
	}
	if aws.Origin != model.OriginTool || aws.ReviewState != model.ReviewUnreviewed {
		t.Fatalf("observation provenance wrong: %+v", aws)
	}
	if !strings.Contains(aws.Title, "verified") {
		t.Fatalf("title = %q", aws.Title)
	}

	gh := obs[1]
	if gh.Severity != "medium" || gh.Location != "/src/app.py:10" || !strings.Contains(gh.Title, "unverified") {
		t.Fatalf("unverified GitHub observation wrong: %+v", gh)
	}
}

func TestTruffleHogEmpty(t *testing.T) {
	obs, err := TruffleHog([]byte(""))
	if err != nil || len(obs) != 0 {
		t.Fatalf("empty output should yield no observations, got %d (%v)", len(obs), err)
	}
}
