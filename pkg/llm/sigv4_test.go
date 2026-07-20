package llm

import (
	"net/http"
	"testing"
	"time"
)

// TestSignV4Vector checks the signer against AWS's canonical "get-vanilla" SigV4 test vector, the
// published reference every SigV4 implementation is validated against.
func TestSignV4Vector(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/", nil)
	creds := awsCreds{
		AccessKeyID:     "AKIDEXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
	}
	ts := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)
	signV4(req, nil, "service", "us-east-1", creds, ts)

	want := "AWS4-HMAC-SHA256 " +
		"Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, " +
		"SignedHeaders=host;x-amz-date, " +
		"Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"
	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("Authorization mismatch:\n got: %s\nwant: %s", got, want)
	}
	if got := req.Header.Get("X-Amz-Date"); got != "20150830T123600Z" {
		t.Errorf("X-Amz-Date = %q", got)
	}
}

func TestSignV4SessionToken(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/", nil)
	creds := awsCreds{AccessKeyID: "AKID", SecretAccessKey: "secret", SessionToken: "tok123"}
	signV4(req, nil, "bedrock", "us-east-1", creds, time.Unix(0, 0))
	if req.Header.Get("X-Amz-Security-Token") != "tok123" {
		t.Error("session token not set as X-Amz-Security-Token")
	}
	// The security token must be a signed header when present.
	if auth := req.Header.Get("Authorization"); !contains(auth, "x-amz-security-token") {
		t.Errorf("x-amz-security-token missing from SignedHeaders: %s", auth)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
