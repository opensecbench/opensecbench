package llm

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
	"time"
)

// awsCreds are the credentials for an AWS SigV4-signed request (ADR-0052, Bedrock). SessionToken is set
// only for temporary/role credentials.
type awsCreds struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// signV4 signs an HTTP request with AWS Signature Version 4 (the scheme Bedrock requires). It mutates req:
// setting Host, X-Amz-Date, the optional X-Amz-Security-Token, and the Authorization header. body is the
// exact request body bytes (nil/empty for GET). Implemented against the stdlib to avoid an AWS SDK dep;
// verified against AWS's canonical SigV4 test vectors (sigv4_test.go).
func signV4(req *http.Request, body []byte, service, region string, c awsCreds, t time.Time) {
	t = t.UTC()
	amzDate := t.Format("20060102T150405Z")
	dateStamp := t.Format("20060102")

	req.Header.Set("Host", req.URL.Host)
	req.Header.Set("X-Amz-Date", amzDate)
	if c.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", c.SessionToken)
	}
	payloadHash := sha256Hex(body)

	// Canonical headers: host + every x-amz-* header, lowercased and sorted.
	signed := []string{"host"}
	values := map[string]string{"host": req.URL.Host}
	for k, vs := range req.Header {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "x-amz-") {
			signed = append(signed, lk)
			values[lk] = strings.TrimSpace(strings.Join(vs, ","))
		}
	}
	sort.Strings(signed)
	var canonHeaders strings.Builder
	for _, h := range signed {
		canonHeaders.WriteString(h)
		canonHeaders.WriteByte(':')
		canonHeaders.WriteString(values[h])
		canonHeaders.WriteByte('\n')
	}
	signedHeaders := strings.Join(signed, ";")

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL.Path),
		canonicalQuery(req.URL.RawQuery),
		canonHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := strings.Join([]string{dateStamp, region, service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	kDate := hmacSHA256([]byte("AWS4"+c.SecretAccessKey), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 "+
		"Credential="+c.AccessKeyID+"/"+scope+", "+
		"SignedHeaders="+signedHeaders+", "+
		"Signature="+signature)
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

// canonicalURI returns the path for canonicalization. Paths are already normalized for the Bedrock
// endpoints we call, so an empty path becomes "/". (Full RFC-3986 segment encoding isn't needed for the
// fixed control/runtime paths used here.)
func canonicalURI(path string) string {
	if path == "" {
		return "/"
	}
	return path
}

// canonicalQuery sorts query parameters by key for the canonical request. The Bedrock calls here use no
// query string, so this handles the empty and simple-sorted cases.
func canonicalQuery(raw string) string {
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, "&")
	sort.Strings(parts)
	return strings.Join(parts, "&")
}
