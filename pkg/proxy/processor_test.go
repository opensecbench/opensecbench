package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeProcessor struct {
	needsResp bool
	onReq     func(m, u, h, b string) (string, string, string, string)
	onResp    func(s int, h, b string) (int, string, string)
}

func (f fakeProcessor) NeedsResponseBody() bool { return f.needsResp }
func (f fakeProcessor) ProcessRequest(m, u, h, b string) (string, string, string, string) {
	if f.onReq != nil {
		return f.onReq(m, u, h, b)
	}
	return m, u, h, b
}
func (f fakeProcessor) ProcessResponse(s int, h, b string) (int, string, string) {
	if f.onResp != nil {
		return f.onResp(s, h, b)
	}
	return s, h, b
}

func TestProcessorTransformsRequestAndResponse(t *testing.T) {
	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte("orig-resp"))
	}))
	defer upstream.Close()

	proc := fakeProcessor{
		needsResp: true,
		onReq: func(m, u, h, b string) (string, string, string, string) {
			return m, u, h, strings.ReplaceAll(b, "SECRET", "REDACTED")
		},
		onResp: func(s int, h, b string) (int, string, string) { return s, h, strings.ReplaceAll(b, "orig", "MODIFIED") },
	}
	client, closeProxy := proxyClient(t, New(nil, nil, nil, nil, proc))
	defer closeProxy()

	resp, err := client.Post(upstream.URL+"/x", "text/plain", strings.NewReader("token=SECRET"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if gotBody != "token=REDACTED" {
		t.Fatalf("upstream received %q, want the request-rule applied", gotBody)
	}
	if string(body) != "MODIFIED-resp" {
		t.Fatalf("client received %q, want the response-rule applied", body)
	}
}
