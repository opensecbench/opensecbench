package httpfilter

import "testing"

func req(url, headers, body string) Input {
	return Input{Phase: "request", Method: "POST", URL: url, Headers: headers, Body: body}
}

func TestEmptyMatchesEverything(t *testing.T) {
	f, err := Compile("")
	if err != nil {
		t.Fatal(err)
	}
	if !f.Match(req("http://x/", "", "")) {
		t.Fatal("empty filter should match everything")
	}
	var nilF *Filter
	if !nilF.Match(req("http://x/", "", "")) {
		t.Fatal("nil filter should match everything")
	}
}

func TestFieldMatching(t *testing.T) {
	in := req("https://api.acme.example/login", "Content-Type: application/json\nAuthorization: Bearer abc", `{"user":{"role":"admin"}}`)
	cases := []struct {
		expr string
		want bool
	}{
		{`method == "POST"`, true},
		{`host == "api.acme.example"`, true},
		{`host.endsWith("acme.example")`, true},
		{`scheme == "https"`, true},
		{`path == "/login"`, true},
		{`content_type.contains("json")`, true},
		{`header["authorization"].startsWith("Bearer ")`, true},
		{`json.user.role == "admin"`, true},
		{`has(json.user) && json.user.role == "admin"`, true},
		{`json.user.role == "guest"`, false},
		{`!path.matches("(?i)\\.(png|css|js)$")`, true},
		{`method == "GET"`, false},
	}
	for _, c := range cases {
		f, err := Compile(c.expr)
		if err != nil {
			t.Fatalf("compile %q: %v", c.expr, err)
		}
		if got := f.Match(in); got != c.want {
			t.Errorf("%q = %v, want %v", c.expr, got, c.want)
		}
	}
}

func TestStaticAssetExcludedByExtension(t *testing.T) {
	f, _ := Compile(`!path.matches("(?i)\\.(css|js|png|jpg|gif|svg|ico|woff2?)$")`)
	if f.Match(Input{Phase: "request", Method: "GET", URL: "https://x/logo.png"}) {
		t.Fatal("image should be excluded")
	}
	if !f.Match(Input{Phase: "request", Method: "GET", URL: "https://x/api/users"}) {
		t.Fatal("api path should match")
	}
}

func TestNonJSONBodyIsCleanNoMatch(t *testing.T) {
	// Addressing a JSON field on a non-JSON body must not error into a match.
	f, _ := Compile(`json.role == "admin"`)
	if f.Match(req("http://x/", "", "not json at all")) {
		t.Fatal("non-JSON body should not match a json.* filter")
	}
}

func TestBadExpressionReported(t *testing.T) {
	if _, err := Compile(`method =`); err == nil {
		t.Fatal("expected a compile error for bad syntax")
	}
	if _, err := Compile(`method`); err == nil {
		t.Fatal("expected an error: expression must be boolean")
	}
}
