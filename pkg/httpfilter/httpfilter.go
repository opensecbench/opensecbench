// Package httpfilter compiles Wireshark-style display filters for HTTP traffic using CEL (Google's
// Common Expression Language). One compiled Filter gates intercept holds and can also filter captured
// history — a single expression language over method, host, path, headers, body, and decoded JSON.
//
// Available fields (all lower-case):
//
//	phase         "request" | "response"
//	method        e.g. "GET", "POST"
//	url           full URL string
//	host          URL hostname
//	path          URL path
//	scheme        "http" | "https"
//	status        response status code (0 at the request phase)
//	content_type  the Content-Type header value (convenience)
//	header        map of lower-cased header name -> value (phase-appropriate)
//	body          request or response body text (phase-appropriate)
//	json          the body decoded as JSON (object/array/scalar); an empty object when the body is not JSON
//
// Examples:
//
//	method == "POST" && content_type.contains("json")
//	host.endsWith("acme.example") && !path.matches("(?i)\\.(png|css|js|woff2?)$")
//	header["authorization"].startsWith("Bearer ")
//	has(json.user) && json.user.role == "admin"
package httpfilter

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/cel-go/cel"
)

// Input is one request/response hold (or a captured exchange) to evaluate a Filter against. Headers is
// the same "Key: value\n" text the rest of the toolset uses; Body/Headers are phase-appropriate.
type Input struct {
	Phase   string
	Method  string
	URL     string
	Status  int
	Headers string
	Body    string
}

// Filter is a compiled CEL predicate over HTTP traffic. A nil *Filter, and a Filter compiled from an
// empty expression, match everything.
type Filter struct {
	expr string
	prg  cel.Program
}

var env *cel.Env

func init() {
	// Errors here would be a programming error in the declarations below, not user input.
	env, _ = cel.NewEnv(
		cel.Variable("phase", cel.StringType),
		cel.Variable("method", cel.StringType),
		cel.Variable("url", cel.StringType),
		cel.Variable("host", cel.StringType),
		cel.Variable("path", cel.StringType),
		cel.Variable("scheme", cel.StringType),
		cel.Variable("status", cel.IntType),
		cel.Variable("content_type", cel.StringType),
		cel.Variable("header", cel.MapType(cel.StringType, cel.StringType)),
		cel.Variable("body", cel.StringType),
		cel.Variable("json", cel.DynType),
	)
}

// Compile builds a Filter from a CEL expression. Empty/whitespace matches everything. The expression
// must evaluate to a bool, or Compile returns an error suitable for showing the operator.
func Compile(expr string) (*Filter, error) {
	if strings.TrimSpace(expr) == "" {
		return &Filter{}, nil
	}
	ast, iss := env.Compile(expr)
	if iss != nil && iss.Err() != nil {
		return nil, iss.Err()
	}
	if ast.OutputType() != cel.BoolType {
		return nil, fmt.Errorf("filter must be a true/false expression, not %s", ast.OutputType())
	}
	prg, err := env.Program(ast)
	if err != nil {
		return nil, err
	}
	return &Filter{expr: expr, prg: prg}, nil
}

// Expr returns the source expression ("" for a match-everything filter).
func (f *Filter) Expr() string {
	if f == nil {
		return ""
	}
	return f.expr
}

// Match reports whether in satisfies the filter. A nil/empty filter matches everything. A runtime
// error (e.g. addressing a JSON field the body doesn't have) counts as no match, so a mistaken filter
// fails safe — it holds nothing rather than everything.
func (f *Filter) Match(in Input) bool {
	if f == nil || f.prg == nil {
		return true
	}
	out, _, err := f.prg.Eval(activation(in))
	if err != nil {
		return false
	}
	b, ok := out.Value().(bool)
	return ok && b
}

func activation(in Input) map[string]any {
	headers := parseHeaders(in.Headers)
	var host, path, scheme string
	if u, err := url.Parse(in.URL); err == nil {
		host, path, scheme = u.Hostname(), u.Path, u.Scheme
	}
	var decoded any = map[string]any{} // non-JSON body -> empty object so json.x is a clean no-match, not an error
	_ = json.Unmarshal([]byte(in.Body), &decoded)
	return map[string]any{
		"phase":        in.Phase,
		"method":       in.Method,
		"url":          in.URL,
		"host":         host,
		"path":         path,
		"scheme":       scheme,
		"status":       in.Status,
		"content_type": headers["content-type"],
		"header":       headers,
		"body":         in.Body,
		"json":         decoded,
	}
}

// parseHeaders turns "Key: value\n" text into a lower-cased-key map (last value wins on duplicates).
func parseHeaders(s string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		m[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	return m
}
