// Command osb is the OpenSecBench command-line client. It is a thin client against the
// control-plane HTTP API (ADR-0001).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/opensecbench/opensecbench/pkg/browser"
	"github.com/opensecbench/opensecbench/pkg/client"
	"github.com/opensecbench/opensecbench/pkg/version"
)

func main() {
	addr := flag.String("addr", "http://127.0.0.1:7373", "control-plane API base URL")
	flag.Usage = usage
	flag.Parse()

	if err := dispatch(context.Background(), client.New(*addr), flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func dispatch(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("no command given")
	}
	switch args[0] {
	case "version":
		fmt.Printf("osb %s\n", version.Version)
		return nil
	case "health":
		h, err := c.Health(ctx)
		if err != nil {
			return err
		}
		return printJSON(h)
	case "search":
		if len(args) < 2 {
			return errors.New("usage: osb search <query>")
		}
		results, err := c.Search(ctx, strings.Join(args[1:], " "))
		if err != nil {
			return err
		}
		for _, r := range results {
			fmt.Printf("%-12s %s  %s\n", r.Kind, r.Title, r.Detail)
		}
		return nil
	case "project":
		return projectCmd(ctx, c, args[1:])
	case "template":
		return templateCmd(ctx, c, args[1:])
	case "capability", "cap":
		return capabilityCmd(ctx, c, args[1:])
	case "playbook":
		return playbookCmd(ctx, c, args[1:])
	case "task":
		return taskCmd(ctx, c, args[1:])
	case "artifact":
		return artifactCmd(ctx, c, args[1:])
	case "application", "app":
		return applicationCmd(ctx, c, args[1:])
	case "asset":
		return assetCmd(ctx, c, args[1:])
	case "context":
		return contextCmd(ctx, c, args[1:])
	case "scope":
		return scopeCmd(ctx, c, args[1:])
	case "repeater":
		return repeaterCmd(ctx, c, args[1:])
	case "session":
		return sessionCmd(ctx, c, args[1:])
	case "audit":
		return auditCmd(ctx, c, args[1:])
	case "notifications", "notif":
		return notifyCmd(ctx, c, args[1:])
	case "report":
		return reportCmd(ctx, c, args[1:])
	case "methodology", "method":
		return methodologyCmd(ctx, c, args[1:])
	case "proxy":
		return proxyCmd(ctx, c, args[1:])
	case "observation", "obs":
		return observationCmd(ctx, c, args[1:])
	case "finding":
		return findingCmd(ctx, c, args[1:])
	case "analyst":
		return analystCmd(ctx, c, args[1:])
	case "thread":
		return threadCmd(ctx, c, args[1:])
	case "approval":
		return approvalCmd(ctx, c, args[1:])
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// paramFlags collects repeated --param key=value flags.
type paramFlags []string

func (p *paramFlags) String() string { return strings.Join(*p, ",") }
func (p *paramFlags) Set(v string) error {
	*p = append(*p, v)
	return nil
}

func (p paramFlags) toMap() (map[string]any, error) {
	if len(p) == 0 {
		return nil, nil
	}
	m := make(map[string]any, len(p))
	for _, kv := range p {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			return nil, fmt.Errorf("bad --param %q (want key=value)", kv)
		}
		m[kv[:i]] = kv[i+1:]
	}
	return m, nil
}

func capabilityCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb capability <list|run>")
	}
	switch args[0] {
	case "list":
		caps, err := c.ListCapabilities(ctx)
		if err != nil {
			return err
		}
		for _, m := range caps {
			fmt.Printf("%-18s %-8s %s\n", m.ID, m.Version, m.Title)
		}
		return nil
	case "run":
		fs := flag.NewFlagSet("capability run", flag.ContinueOnError)
		id := fs.String("id", "", "capability id (required)")
		dir := fs.String("dir", "", "target directory to analyze")
		asset := fs.String("asset", "", "asset id to target (source_repo) instead of --dir")
		project := fs.String("project", "", "project id for scope enforcement (network capabilities)")
		actor := fs.String("actor", "", "actor label (e.g. human:james)")
		var params paramFlags
		fs.Var(&params, "param", "capability parameter as key=value (repeatable)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *id == "" {
			return errors.New("capability run: --id is required")
		}
		p, err := params.toMap()
		if err != nil {
			return err
		}
		req := client.RunTaskRequest{CapabilityID: *id, TargetDir: *dir, Actor: *actor, Params: p}
		if *asset != "" {
			req.AssetID = asset
		}
		if *project != "" {
			req.ProjectID = project
		}
		out, err := c.RunTask(ctx, req)
		if err != nil {
			return err
		}
		return printJSON(out)
	default:
		return fmt.Errorf("unknown capability subcommand %q", args[0])
	}
}

func playbookCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb playbook <list|run>")
	}
	switch args[0] {
	case "list":
		pbs, err := c.ListPlaybooks(ctx)
		if err != nil {
			return err
		}
		for _, p := range pbs {
			steps := make([]string, 0, len(p.Steps))
			for _, s := range p.Steps {
				steps = append(steps, s.Capability)
			}
			fmt.Printf("%-16s %-22s [%s]\n", p.ID, p.Name, strings.Join(steps, ", "))
		}
		return nil
	case "run":
		fs := flag.NewFlagSet("playbook run", flag.ContinueOnError)
		id := fs.String("id", "", "playbook id (required)")
		asset := fs.String("asset", "", "asset id (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *id == "" || *asset == "" {
			return errors.New("playbook run: --id and --asset are required")
		}
		res, err := c.RunPlaybook(ctx, *id, *asset)
		if err != nil {
			return err
		}
		return printJSON(res)
	default:
		return fmt.Errorf("unknown playbook subcommand %q", args[0])
	}
}

func taskCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 2 {
		return errors.New("usage: osb task <get|cancel> <id>")
	}
	switch args[0] {
	case "get":
		t, err := c.GetTask(ctx, args[1])
		if err != nil {
			return err
		}
		return printJSON(t)
	case "cancel":
		if err := c.CancelTask(ctx, args[1]); err != nil {
			return err
		}
		fmt.Println("cancelled", args[1])
		return nil
	default:
		return fmt.Errorf("unknown task subcommand %q", args[0])
	}
}

func analystCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 || args[0] != "ask" {
		return errors.New("usage: osb analyst ask <message>")
	}
	message := strings.Join(args[1:], " ")
	if message == "" {
		return errors.New("analyst ask: a message is required")
	}
	res, err := c.AnalystAsk(ctx, message)
	if err != nil {
		return err
	}
	return printSendResult(res)
}

func threadCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb thread <list|show|send>")
	}
	switch args[0] {
	case "list":
		ts, err := c.ListThreads(ctx)
		if err != nil {
			return err
		}
		for _, t := range ts {
			title := t.Title
			if title == "" {
				title = "(untitled)"
			}
			fmt.Printf("%s  %-18s %s\n", t.ID, t.Status, title)
		}
		return nil
	case "show":
		if len(args) < 2 {
			return errors.New("usage: osb thread show <id>")
		}
		d, err := c.GetThread(ctx, args[1])
		if err != nil {
			return err
		}
		for _, m := range d.Messages {
			if m.Role == "system" {
				continue
			}
			fmt.Printf("[%s] %s\n", m.Role, m.Content)
		}
		return nil
	case "send":
		if len(args) < 3 {
			return errors.New("usage: osb thread send <id> <message>")
		}
		res, err := c.SendMessage(ctx, args[1], strings.Join(args[2:], " "))
		if err != nil {
			return err
		}
		return printSendResult(res)
	default:
		return fmt.Errorf("unknown thread subcommand %q", args[0])
	}
}

func approvalCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb approval <list|approve|deny>")
	}
	switch args[0] {
	case "list":
		aps, err := c.ListApprovals(ctx)
		if err != nil {
			return err
		}
		if len(aps) == 0 {
			fmt.Println("(no pending approvals)")
			return nil
		}
		for _, a := range aps {
			fmt.Printf("%s  %s %s  (thread %s)\n", a.ID, a.Tool, string(a.Args), a.ThreadID)
		}
		return nil
	case "approve", "deny":
		if len(args) < 2 {
			return fmt.Errorf("usage: osb approval %s <id>", args[0])
		}
		res, err := c.DecideApproval(ctx, args[1], args[0])
		if err != nil {
			return err
		}
		return printSendResult(res)
	default:
		return fmt.Errorf("unknown approval subcommand %q", args[0])
	}
}

// printSendResult renders an Analyst turn: tool activity, then an answer or a pending approval.
func printSendResult(res client.SendResult) error {
	for _, m := range res.NewMessages {
		if m.Role == "user" && strings.HasPrefix(m.Content, "Tool ") {
			line := m.Content
			if i := strings.IndexByte(line, '\n'); i >= 0 {
				line = line[:i]
			}
			fmt.Printf("  · %s\n", line)
		}
	}
	if res.Pending != nil {
		fmt.Printf("⏸  awaiting approval — %s %s\n", res.Pending.Tool, string(res.Pending.Args))
		fmt.Printf("   approve: osb approval approve %s\n", res.Pending.ID)
		fmt.Printf("   deny:    osb approval deny %s\n", res.Pending.ID)
	} else {
		fmt.Println(res.Answer)
	}
	fmt.Printf("   (thread %s)\n", res.Thread.ID)
	return nil
}

func templateCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 || args[0] != "list" {
		return errors.New("usage: osb template list")
	}
	tmpls, err := c.ListTemplates(ctx)
	if err != nil {
		return err
	}
	for _, t := range tmpls {
		fmt.Printf("%-12s %-22s %s\n", t.ID, t.Name, t.Description)
	}
	return nil
}

func applicationCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb application <list|create>")
	}
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("application list", flag.ContinueOnError)
		project := fs.String("project", "", "project id (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *project == "" {
			return errors.New("application list: --project is required")
		}
		apps, err := c.ListApplications(ctx, *project)
		if err != nil {
			return err
		}
		for _, a := range apps {
			fmt.Printf("%s  %s\n", a.ID, a.Name)
		}
		return nil
	case "create":
		fs := flag.NewFlagSet("application create", flag.ContinueOnError)
		project := fs.String("project", "", "project id (required)")
		name := fs.String("name", "", "application name (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *project == "" || *name == "" {
			return errors.New("application create: --project and --name are required")
		}
		app, err := c.CreateApplication(ctx, *project, *name)
		if err != nil {
			return err
		}
		return printJSON(app)
	default:
		return fmt.Errorf("unknown application subcommand %q", args[0])
	}
}

func assetCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb asset <list|create>")
	}
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("asset list", flag.ContinueOnError)
		app := fs.String("app", "", "application id (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *app == "" {
			return errors.New("asset list: --app is required")
		}
		assets, err := c.ListAssets(ctx, *app)
		if err != nil {
			return err
		}
		for _, a := range assets {
			fmt.Printf("%s  %-16s %-11s %s\n", a.ID, a.Type, a.Sensitivity, a.Location)
		}
		return nil
	case "create":
		fs := flag.NewFlagSet("asset create", flag.ContinueOnError)
		app := fs.String("app", "", "application id (required)")
		typ := fs.String("type", "source_repo", "asset type")
		location := fs.String("location", "", "asset location/path (required)")
		sensitivity := fs.String("sensitivity", "", "open_source | private (inferred if empty)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *app == "" || *location == "" {
			return errors.New("asset create: --app and --location are required")
		}
		asset, err := c.CreateAsset(ctx, *app, client.CreateAssetRequest{
			Type:        *typ,
			Location:    *location,
			Sensitivity: *sensitivity,
		})
		if err != nil {
			return err
		}
		return printJSON(asset)
	default:
		return fmt.Errorf("unknown asset subcommand %q", args[0])
	}
}

func contextCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb context <list|add>")
	}
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("context list", flag.ContinueOnError)
		project := fs.String("project", "", "project id (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *project == "" {
			return errors.New("context list: --project is required")
		}
		items, err := c.ListContext(ctx, *project)
		if err != nil {
			return err
		}
		for _, ci := range items {
			fmt.Printf("%s  %-10s %s\n", ci.ID, ci.Type, ci.Name)
		}
		return nil
	case "add":
		fs := flag.NewFlagSet("context add", flag.ContinueOnError)
		project := fs.String("project", "", "project id (required)")
		file := fs.String("file", "", "path to the file to ingest (required)")
		ctype := fs.String("type", "document", "context type: document | email | chat | note")
		name := fs.String("name", "", "display name (default: file base name)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *project == "" || *file == "" {
			return errors.New("context add: --project and --file are required")
		}
		data, err := os.ReadFile(*file)
		if err != nil {
			return err
		}
		displayName := *name
		if displayName == "" {
			displayName = filepath.Base(*file)
		}
		ci, err := c.IngestContext(ctx, *project, displayName, *ctype, mediaTypeForExt(*file), data)
		if err != nil {
			return err
		}
		return printJSON(ci)
	default:
		return fmt.Errorf("unknown context subcommand %q", args[0])
	}
}

func scopeCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb scope <list|add|delete>")
	}
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("scope list", flag.ContinueOnError)
		project := fs.String("project", "", "project id (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *project == "" {
			return errors.New("scope list: --project is required")
		}
		entries, err := c.ListScope(ctx, *project)
		if err != nil {
			return err
		}
		for _, e := range entries {
			fmt.Printf("%s  %-7s %s\n", e.ID, e.Kind, e.Value)
		}
		return nil
	case "add":
		fs := flag.NewFlagSet("scope add", flag.ContinueOnError)
		project := fs.String("project", "", "project id (required)")
		kind := fs.String("kind", "", "entry kind: host | domain | cidr (required)")
		value := fs.String("value", "", "the host, domain, or CIDR (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *project == "" || *kind == "" || *value == "" {
			return errors.New("scope add: --project, --kind, and --value are required")
		}
		entry, err := c.AddScope(ctx, *project, *kind, *value)
		if err != nil {
			return err
		}
		return printJSON(entry)
	case "delete", "rm":
		fs := flag.NewFlagSet("scope delete", flag.ContinueOnError)
		id := fs.String("id", "", "scope entry id (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *id == "" {
			return errors.New("scope delete: --id is required")
		}
		return c.DeleteScope(ctx, *id)
	default:
		return fmt.Errorf("unknown scope subcommand %q", args[0])
	}
}

func repeaterCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb repeater <send|list|get|evidence>")
	}
	switch args[0] {
	case "send":
		fs := flag.NewFlagSet("repeater send", flag.ContinueOnError)
		project := fs.String("project", "", "project id (required)")
		method := fs.String("method", "GET", "HTTP method")
		urlStr := fs.String("url", "", "request URL (required)")
		name := fs.String("name", "", "label for the exchange")
		body := fs.String("body", "", "request body")
		var headers paramFlags
		fs.Var(&headers, "header", "request header as 'Key: value' (repeatable)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *project == "" || *urlStr == "" {
			return errors.New("repeater send: --project and --url are required")
		}
		ex, err := c.CreateExchange(ctx, *project, client.NewExchange{
			Name: *name, Method: *method, URL: *urlStr,
			RequestHeaders: strings.Join(headers, "\n"), RequestBody: *body,
		})
		if err != nil {
			return err
		}
		sent, err := c.SendExchange(ctx, ex.ID)
		if err != nil {
			return err
		}
		return printJSON(sent)
	case "list":
		fs := flag.NewFlagSet("repeater list", flag.ContinueOnError)
		project := fs.String("project", "", "project id (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *project == "" {
			return errors.New("repeater list: --project is required")
		}
		items, err := c.ListExchanges(ctx, *project)
		if err != nil {
			return err
		}
		for _, e := range items {
			status := "—"
			if e.Status != nil {
				status = fmt.Sprint(*e.Status)
			}
			fmt.Printf("%s  %-4s %-3s %s\n", e.ID, e.Method, status, e.URL)
		}
		return nil
	case "get":
		if len(args) < 2 {
			return errors.New("usage: osb repeater get <id>")
		}
		ex, err := c.GetExchange(ctx, args[1])
		if err != nil {
			return err
		}
		return printJSON(ex)
	case "evidence":
		fs := flag.NewFlagSet("repeater evidence", flag.ContinueOnError)
		id := fs.String("id", "", "exchange id (required)")
		note := fs.String("note", "", "note to attach to the observation")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *id == "" {
			return errors.New("repeater evidence: --id is required")
		}
		obs, err := c.SaveExchangeEvidence(ctx, *id, *note)
		if err != nil {
			return err
		}
		return printJSON(obs)
	default:
		return fmt.Errorf("unknown repeater subcommand %q", args[0])
	}
}

// sessionCmd manages interactive terminal sessions. Interactive attach happens in the desktop app
// (over the WebSocket); the CLI covers listing, closing, and evidence.
//
// TODO(P7+): `osb session attach <id>` — a raw-mode terminal client over the WebSocket.
func sessionCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb session <list|open|get|close|evidence>")
	}
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("session list", flag.ContinueOnError)
		project := fs.String("project", "", "project id (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *project == "" {
			return errors.New("session list: --project is required")
		}
		items, err := c.ListSessions(ctx, *project)
		if err != nil {
			return err
		}
		for _, s := range items {
			fmt.Printf("%s  %-7s %s\n", s.ID, s.Status, s.Container)
		}
		return nil
	case "open":
		fs := flag.NewFlagSet("session open", flag.ContinueOnError)
		project := fs.String("project", "", "project id (required)")
		actor := fs.String("actor", "", "actor label (e.g. human:james)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *project == "" {
			return errors.New("session open: --project is required")
		}
		s, err := c.OpenSession(ctx, *project, *actor)
		if err != nil {
			return err
		}
		return printJSON(s)
	case "get":
		if len(args) < 2 {
			return errors.New("usage: osb session get <id>")
		}
		s, err := c.GetSession(ctx, args[1])
		if err != nil {
			return err
		}
		return printJSON(s)
	case "close":
		if len(args) < 2 {
			return errors.New("usage: osb session close <id>")
		}
		s, err := c.CloseSession(ctx, args[1])
		if err != nil {
			return err
		}
		return printJSON(s)
	case "evidence":
		fs := flag.NewFlagSet("session evidence", flag.ContinueOnError)
		id := fs.String("id", "", "session id (required)")
		note := fs.String("note", "", "note to attach to the observation")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *id == "" {
			return errors.New("session evidence: --id is required")
		}
		obs, err := c.SaveSessionEvidence(ctx, *id, *note)
		if err != nil {
			return err
		}
		return printJSON(obs)
	default:
		return fmt.Errorf("unknown session subcommand %q", args[0])
	}
}

func proxyCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb proxy <start|stop|status|ca>")
	}
	switch args[0] {
	case "start":
		fs := flag.NewFlagSet("proxy start", flag.ContinueOnError)
		project := fs.String("project", "", "project id (required)")
		port := fs.Int("port", 0, "loopback port (0 = auto-assign)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *project == "" {
			return errors.New("proxy start: --project is required")
		}
		st, err := c.StartProxy(ctx, *project, *port)
		if err != nil {
			return err
		}
		fmt.Printf("proxy listening on 127.0.0.1:%d — set your client's HTTP(S) proxy there\n", st.Port)
		fmt.Println("trust the CA: osb proxy ca --out osb-ca.crt")
		return nil
	case "stop":
		fs := flag.NewFlagSet("proxy stop", flag.ContinueOnError)
		project := fs.String("project", "", "project id (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *project == "" {
			return errors.New("proxy stop: --project is required")
		}
		if _, err := c.StopProxy(ctx, *project); err != nil {
			return err
		}
		fmt.Println("proxy stopped")
		return nil
	case "status":
		fs := flag.NewFlagSet("proxy status", flag.ContinueOnError)
		project := fs.String("project", "", "project id (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *project == "" {
			return errors.New("proxy status: --project is required")
		}
		st, err := c.GetProxy(ctx, *project)
		if err != nil {
			return err
		}
		return printJSON(st)
	case "ca":
		fs := flag.NewFlagSet("proxy ca", flag.ContinueOnError)
		out := fs.String("out", "", "write the CA cert to this file (default: stdout)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		pem, err := c.ProxyCACert(ctx)
		if err != nil {
			return err
		}
		if *out == "" {
			_, err := os.Stdout.Write(pem)
			return err
		}
		if err := os.WriteFile(*out, pem, 0o600); err != nil {
			return err
		}
		fmt.Printf("wrote CA certificate to %s — trust it in your browser/tools\n", *out)
		return nil
	case "browser":
		return proxyBrowser(ctx, c, args[1:])
	default:
		return fmt.Errorf("unknown proxy subcommand %q", args[0])
	}
}

// proxyBrowser launches an isolated Chromium instance preconfigured to use the project's proxy and
// to trust only its CA (via --ignore-certificate-errors-spki-list), so no system trust change is
// needed — the browser equivalent of Burp's embedded browser.
func proxyBrowser(ctx context.Context, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("proxy browser", flag.ContinueOnError)
	project := fs.String("project", "", "project id (required)")
	startURL := fs.String("url", "about:blank", "initial URL to open")
	browserBin := fs.String("browser", "", "browser binary to use (default: autodetect; or set OSB_BROWSER)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *project == "" {
		return errors.New("proxy browser: --project is required")
	}

	st, err := c.StartProxy(ctx, *project, 0) // idempotent: returns the running proxy if any
	if err != nil {
		return err
	}
	if st.CASPKI == "" {
		return errors.New("proxy CA unavailable; cannot configure browser trust")
	}
	bin, err := browser.Resolve(*browserBin)
	if err != nil {
		return err
	}

	profile, err := os.MkdirTemp("", "osb-browser-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(profile) }()

	// Ctrl-C closes the browser and cleans up.
	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	chromeArgs := []string{
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--no-default-browser-check",
		fmt.Sprintf("--proxy-server=127.0.0.1:%d", st.Port),
		"--proxy-bypass-list=<-loopback>", // also route loopback through the proxy (assess local apps)
		"--ignore-certificate-errors-spki-list=" + st.CASPKI,
		*startURL,
	}
	fmt.Printf("launching %s → proxy 127.0.0.1:%d\n", filepath.Base(bin), st.Port)
	fmt.Println("(isolated throwaway profile; trusts the OpenSecBench CA only — no system trust change)")
	fmt.Println("close the browser (or Ctrl-C) to return.")

	cmd := exec.CommandContext(runCtx, bin, chromeArgs...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil && runCtx.Err() == nil {
		return fmt.Errorf("launch browser: %w", err)
	}
	return nil
}

func methodologyCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb methodology <catalog|coverage|adopt|set>")
	}
	switch args[0] {
	case "catalog":
		packs, err := c.ListMethodologies(ctx)
		if err != nil {
			return err
		}
		for _, p := range packs {
			fmt.Printf("%s  (%s) — %s [%d items]\n", p.ID, p.Tech, p.Title, len(p.Items))
			for _, it := range p.Items {
				fmt.Printf("    %s  %s\n", it.ID, it.Title)
			}
		}
		return nil
	case "coverage":
		fs := flag.NewFlagSet("methodology coverage", flag.ContinueOnError)
		project := fs.String("project", "", "project id (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *project == "" {
			return errors.New("methodology coverage: --project is required")
		}
		cov, err := c.GetMethodologyCoverage(ctx, *project)
		if err != nil {
			return err
		}
		fmt.Printf("coverage: %d%% (%d/%d covered)\n", cov.Summary.CoveredPct, cov.Summary.Covered, cov.Summary.Total)
		for _, p := range cov.Packs {
			fmt.Printf("\n%s — %s\n", p.ID, p.Title)
			for _, it := range p.Items {
				fmt.Printf("  [%-14s] %s\n", it.Status, it.Item.Title)
			}
		}
		return nil
	case "adopt":
		fs := flag.NewFlagSet("methodology adopt", flag.ContinueOnError)
		project := fs.String("project", "", "project id (required)")
		id := fs.String("id", "", "methodology pack id (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *project == "" || *id == "" {
			return errors.New("methodology adopt: --project and --id are required")
		}
		if err := c.AdoptMethodology(ctx, *project, *id); err != nil {
			return err
		}
		fmt.Printf("adopted %s\n", *id)
		return nil
	case "set":
		fs := flag.NewFlagSet("methodology set", flag.ContinueOnError)
		project := fs.String("project", "", "project id (required)")
		item := fs.String("item", "", "item id, e.g. web-app/xss (required)")
		status := fs.String("status", "", "not_started | in_progress | covered | not_applicable (required)")
		note := fs.String("note", "", "optional note")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *project == "" || *item == "" || *status == "" {
			return errors.New("methodology set: --project, --item, and --status are required")
		}
		if err := c.SetCoverage(ctx, *project, *item, *status, *note); err != nil {
			return err
		}
		fmt.Printf("%s -> %s\n", *item, *status)
		return nil
	default:
		return fmt.Errorf("unknown methodology subcommand %q", args[0])
	}
}

func reportCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb report <templates|generate|list>")
	}
	switch args[0] {
	case "templates":
		tmpls, err := c.ListReportTemplates(ctx)
		if err != nil {
			return err
		}
		for _, t := range tmpls {
			fmt.Printf("%-12s %-20s %s\n", t.ID, t.Kind, t.Title)
		}
		return nil
	case "generate":
		fs := flag.NewFlagSet("report generate", flag.ContinueOnError)
		project := fs.String("project", "", "project id (required)")
		tmpl := fs.String("template", "technical", "template id (executive | technical)")
		format := fs.String("format", "html", "output format: html | md")
		out := fs.String("out", "", "write the report to this file (default: stdout)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *project == "" {
			return errors.New("report generate: --project is required")
		}
		rep, err := c.GenerateReport(ctx, *project, *tmpl, *format)
		if err != nil {
			return err
		}
		body, err := c.ArtifactContent(ctx, rep.ArtifactID)
		if err != nil {
			return err
		}
		if *out == "" {
			_, err := os.Stdout.Write(body)
			return err
		}
		if err := os.WriteFile(*out, body, 0o600); err != nil {
			return err
		}
		fmt.Printf("wrote %s report to %s\n", rep.TemplateID, *out)
		return nil
	case "list":
		fs := flag.NewFlagSet("report list", flag.ContinueOnError)
		project := fs.String("project", "", "project id (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *project == "" {
			return errors.New("report list: --project is required")
		}
		reps, err := c.ListReports(ctx, *project)
		if err != nil {
			return err
		}
		for _, rep := range reps {
			fmt.Printf("%s  %-12s %-5s %s\n", rep.ID, rep.TemplateID, rep.Format, rep.CreatedAt.Format("2006-01-02 15:04"))
		}
		return nil
	default:
		return fmt.Errorf("unknown report subcommand %q", args[0])
	}
}

func notifyCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb notifications <list|read|read-all|watch>")
	}
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("notifications list", flag.ContinueOnError)
		unread := fs.Bool("unread", false, "only unread")
		limit := fs.Int("limit", 50, "max to show")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		feed, err := c.ListNotifications(ctx, *unread, *limit)
		if err != nil {
			return err
		}
		fmt.Printf("%d unread\n", feed.Unread)
		for _, n := range feed.Notifications {
			mark := " "
			if !n.Read {
				mark = "*"
			}
			fmt.Printf("%s %s  %-9s %s — %s\n", mark, n.ID[:8], n.Kind, n.Title, n.Body)
		}
		return nil
	case "read":
		if len(args) < 2 {
			return errors.New("usage: osb notifications read <id>")
		}
		return c.MarkNotificationRead(ctx, args[1])
	case "read-all":
		return c.MarkAllNotificationsRead(ctx)
	case "watch":
		fs := flag.NewFlagSet("notifications watch", flag.ContinueOnError)
		interval := fs.Int("interval", 10, "poll interval seconds")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return watchNotifications(ctx, c, *interval)
	default:
		return fmt.Errorf("unknown notifications subcommand %q", args[0])
	}
}

// watchNotifications polls for unread notifications and fires an OS-native notification for each new
// one, until interrupted.
func watchNotifications(ctx context.Context, c *client.Client, interval int) error {
	if interval < 2 {
		interval = 2
	}
	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Printf("watching for notifications every %ds (Ctrl-C to stop)\n", interval)

	seen := map[string]bool{}
	first := true
	tick := time.NewTicker(time.Duration(interval) * time.Second)
	defer tick.Stop()
	for {
		feed, err := c.ListNotifications(runCtx, true, 50)
		if err == nil {
			for i := len(feed.Notifications) - 1; i >= 0; i-- { // oldest first
				n := feed.Notifications[i]
				if seen[n.ID] {
					continue
				}
				seen[n.ID] = true
				if !first { // don't replay the backlog on startup
					osNotify(n.Title, n.Body)
				}
			}
			first = false
		}
		select {
		case <-runCtx.Done():
			return nil
		case <-tick.C:
		}
	}
}

// osNotify fires a best-effort native desktop notification.
func osNotify(title, body string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf("display notification %q with title %q", body, "OpenSecBench: "+title)
		cmd = exec.Command("osascript", "-e", script)
	case "windows":
		ps := fmt.Sprintf(`[void][System.Reflection.Assembly]::LoadWithPartialName('System.Windows.Forms');`+
			`$n=New-Object System.Windows.Forms.NotifyIcon;$n.Icon=[System.Drawing.SystemIcons]::Information;`+
			`$n.Visible=$true;$n.ShowBalloonTip(5000,%q,%q,'Info')`, "OpenSecBench: "+title, body)
		cmd = exec.Command("powershell", "-NoProfile", "-Command", ps)
	default:
		cmd = exec.Command("notify-send", "OpenSecBench: "+title, body)
	}
	_ = cmd.Run() // best-effort
}

func auditCmd(ctx context.Context, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	limit := fs.Int("limit", 50, "max events to show")
	jsonOut := fs.Bool("json", false, "emit raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	events, err := c.ListAudit(ctx, *limit)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(events)
	}
	for _, e := range events {
		fmt.Printf("%5d  %s  %-22s %-18s %s\n",
			e.Seq, e.Time.Format("2006-01-02 15:04:05"), e.Action, e.Actor, e.Target)
	}
	return nil
}

func mediaTypeForExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt", ".md", ".log":
		return "text/plain"
	case ".json":
		return "application/json"
	case ".html", ".htm":
		return "text/html"
	case ".eml":
		return "message/rfc822"
	default:
		return ""
	}
}

func observationCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb observation <list|review>")
	}
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("observation list", flag.ContinueOnError)
		taskID := fs.String("task", "", "task id (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *taskID == "" {
			return errors.New("observation list: --task is required")
		}
		obs, err := c.ListTaskObservations(ctx, *taskID)
		if err != nil {
			return err
		}
		for _, o := range obs {
			fmt.Printf("%s  [%s/%s] %-8s %s  %s\n", o.ID, o.Origin, o.ReviewState, o.Severity, o.Title, o.Location)
		}
		return nil
	case "review":
		if len(args) < 2 {
			return errors.New("usage: osb observation review <id> --state confirmed|rejected")
		}
		id := args[1]
		fs := flag.NewFlagSet("observation review", flag.ContinueOnError)
		state := fs.String("state", "", "confirmed | rejected | unreviewed (required)")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if *state == "" {
			return errors.New("observation review: --state is required")
		}
		if err := c.ReviewObservation(ctx, id, *state); err != nil {
			return err
		}
		fmt.Printf("observation %s -> %s\n", id, *state)
		return nil
	default:
		return fmt.Errorf("unknown observation subcommand %q", args[0])
	}
}

func findingCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb finding <list|get|create>")
	}
	switch args[0] {
	case "list":
		findings, err := c.ListFindings(ctx)
		if err != nil {
			return err
		}
		for _, f := range findings {
			fmt.Printf("%s  %-8s %-14s %s\n", f.ID, f.Severity, f.Status, f.Title)
		}
		return nil
	case "get":
		if len(args) < 2 {
			return errors.New("usage: osb finding get <id>")
		}
		f, err := c.GetFinding(ctx, args[1])
		if err != nil {
			return err
		}
		return printJSON(f)
	case "create":
		fs := flag.NewFlagSet("finding create", flag.ContinueOnError)
		title := fs.String("title", "", "finding title (required)")
		severity := fs.String("severity", "", "severity (info|low|medium|high|critical)")
		cwe := fs.String("cwe", "", "CWE identifier")
		var obs paramFlags
		fs.Var(&obs, "obs", "supporting (confirmed) observation id (repeatable)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *title == "" {
			return errors.New("finding create: --title is required")
		}
		f, err := c.CreateFinding(ctx, client.CreateFindingRequest{
			Title:          *title,
			Severity:       *severity,
			CWE:            *cwe,
			ObservationIDs: []string(obs),
		})
		if err != nil {
			return err
		}
		return printJSON(f)
	default:
		return fmt.Errorf("unknown finding subcommand %q", args[0])
	}
}

func artifactCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 2 || args[0] != "get" {
		return errors.New("usage: osb artifact get <id>")
	}
	b, err := c.ArtifactContent(ctx, args[1])
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(b)
	return err
}

func projectCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb project <list|create|get|delete>")
	}
	switch args[0] {
	case "list":
		projects, err := c.ListProjects(ctx)
		if err != nil {
			return err
		}
		return printJSON(projects)
	case "get":
		if len(args) < 2 {
			return errors.New("usage: osb project get <id>")
		}
		p, err := c.GetProject(ctx, args[1])
		if err != nil {
			return err
		}
		return printJSON(p)
	case "delete":
		if len(args) < 2 {
			return errors.New("usage: osb project delete <id>")
		}
		if err := c.DeleteProject(ctx, args[1]); err != nil {
			return err
		}
		fmt.Println("deleted", args[1])
		return nil
	case "create":
		fs := flag.NewFlagSet("project create", flag.ContinueOnError)
		name := fs.String("name", "", "project name (required)")
		tmpl := fs.String("template", "", "scaffold from a template (see 'osb template list')")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *name == "" {
			return errors.New("project create: --name is required")
		}
		if *tmpl != "" {
			res, err := c.CreateProjectFromTemplate(ctx, *tmpl, *name)
			if err != nil {
				return err
			}
			return printJSON(res)
		}
		p, err := c.CreateProject(ctx, client.CreateProjectRequest{Name: *name})
		if err != nil {
			return err
		}
		return printJSON(p)
	default:
		return fmt.Errorf("unknown project subcommand %q", args[0])
	}
}

func printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

func usage() {
	fmt.Fprint(os.Stderr, `osb — OpenSecBench CLI

Usage:
  osb [--addr URL] <command>

Commands:
  version                     print the client version
  health                      check the control plane
  search <query>              search across projects, apps, assets, findings, observations
  project list                list projects
  project get <id>            show a project
  project create --name NAME [--template ID]  create a project
  project delete <id>         delete a project
  template list               list project templates
  application create --project ID --name NAME
  application list --project ID
  asset create --app ID --type source_repo --location PATH [--sensitivity S]
  asset list --app ID
  context add --project ID --file PATH [--type document] [--name NAME]
  context list --project ID
  scope add --project ID --kind host|domain|cidr --value V  add an in-scope allowlist entry
  scope list --project ID     list a project's scope allowlist
  scope delete --id ID        remove a scope entry
  repeater send --project ID --url URL [--method M] [--header 'K: v'] [--body B]  send an HTTP request
  repeater list --project ID  list HTTP exchanges
  repeater get <id>           show an exchange (request + response)
  repeater evidence --id ID [--note N]  save a response as evidence (observation)
  session open --project ID   open a sandboxed terminal (attach in the desktop app)
  session list --project ID   list terminal sessions
  session close <id>          close a session and capture its transcript
  session evidence --id ID [--note N]  save a session transcript as evidence
  methodology catalog        list methodology packs + items
  methodology adopt --project ID --id PACK   adopt a methodology pack
  methodology coverage --project ID          show coverage + roll-up
  methodology set --project ID --item ID --status S [--note N]  set item status
  report templates           list report templates
  report generate --project ID [--template T] [--format html|md] [--out FILE]
  report list --project ID    list generated reports
  audit [--limit N] [--json]  show the append-only audit trail
  notifications list [--unread]  show notifications
  notifications watch          fire OS-native notifications as they arrive
  notifications read-all       mark all read
  proxy start --project ID [--port N]  start the intercepting proxy for a project
  proxy stop --project ID     stop the proxy
  proxy ca [--out FILE]        fetch the proxy CA cert to trust
  proxy browser --project ID [--url U]  launch a preconfigured throwaway browser
  capability list             list available capabilities
  capability run --id ID (--dir PATH | --asset ID) [--project ID] [--param k=v]  run a capability
  playbook list               list playbooks
  playbook run --id ID --asset ID  run a playbook against an asset
  task get <id>               show a task
  task cancel <id>            stop a running task
  artifact get <id>           write an artifact's bytes to stdout
  observation list --task ID  list a task's observations
  observation review <id> --state confirmed|rejected
  finding create --title T [--severity S] [--cwe C] [--obs ID ...]
  finding list                list findings
  finding get <id>            show a finding
  analyst ask <message>       ask the Analyst (needs OSB_LLM_PROVIDER on the daemon)
  thread list                 list Analyst threads
  thread show <id>            show a thread's messages
  thread send <id> <message>  continue a thread
  approval list               list pending approvals
  approval approve <id>       approve a gated action and resume
  approval deny <id>          deny a gated action and resume
`)
}
