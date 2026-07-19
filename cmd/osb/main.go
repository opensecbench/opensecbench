// Command osb is the OpenSecBench command-line client. It is a thin client against the
// control-plane HTTP API (ADR-0001).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/opensecbench/opensecbench/pkg/browser"
	"github.com/opensecbench/opensecbench/pkg/bundle"
	"github.com/opensecbench/opensecbench/pkg/client"
	"github.com/opensecbench/opensecbench/pkg/extension"
	"github.com/opensecbench/opensecbench/pkg/hub"
	"github.com/opensecbench/opensecbench/pkg/model"
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
	case "runner":
		return runnerCmd(ctx, c, args[1:])
	case "integration", "integrations":
		return integrationCmd(ctx, c, args[1:])
	case "investigation", "investigations":
		return investigationCmd(ctx, c, args[1:])
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
	case "replay":
		return replayCmd(ctx, c, args[1:])
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
	case "kb":
		return kbCmd(ctx, c, args[1:])
	case "secret":
		return secretCmd(ctx, c, args[1:])
	case "canary":
		return canaryCmd(ctx, c, args[1:])
	case "dlp":
		return dlpCmd(ctx, c, args[1:])
	case "proxy":
		return proxyCmd(ctx, c, args[1:])
	case "ext", "extension":
		return extCmd(ctx, c, args[1:])
	case "hub":
		return hubCmd(ctx, c, args[1:])
	case "policy":
		return policyCmd(ctx, c, args[1:])
	case "rag":
		return ragCmd(ctx, c, args[1:])
	case "dossier":
		return dossierCmd(ctx, c, args[1:])
	case "plan":
		return planCmd(ctx, c, args[1:])
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
		runnerID := fs.String("runner", "", "run on an enrolled remote runner id instead of the local host")
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
		req := client.RunTaskRequest{CapabilityID: *id, TargetDir: *dir, Actor: *actor, Params: p, RunnerID: *runnerID}
		if *asset != "" {
			req.AssetID = asset
		}
		if *project != "" {
			req.ProjectID = project
		}
		// The task runs asynchronously (ADR-0022): enqueue, then poll to completion.
		t, err := c.RunTask(ctx, req)
		if err != nil {
			return err
		}
		for t.Status == "pending" || t.Status == "running" {
			time.Sleep(time.Second)
			if t, err = c.GetTask(ctx, t.ID); err != nil {
				return err
			}
		}
		obs, _ := c.ListTaskObservations(ctx, t.ID)
		return printJSON(map[string]any{"task": t, "observations": obs})
	default:
		return fmt.Errorf("unknown capability subcommand %q", args[0])
	}
}

func investigationCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 2 {
		return errors.New("usage: osb investigation <list <project> | run|resolve|dismiss <id>>")
	}
	switch args[0] {
	case "list":
		invs, err := c.ListInvestigations(ctx, args[1])
		if err != nil {
			return err
		}
		for _, inv := range invs {
			fmt.Printf("%-36s %-14s %s\n", inv.ID, inv.Status, inv.Title)
		}
		return nil
	case "run":
		if err := c.RunInvestigation(ctx, args[1]); err != nil {
			return err
		}
		fmt.Printf("investigation %s started (see the analyst thread)\n", args[1])
		return nil
	case "resolve", "dismiss":
		status := map[string]string{"resolve": "resolved", "dismiss": "dismissed"}[args[0]]
		if err := c.SetInvestigationStatus(ctx, args[1], status); err != nil {
			return err
		}
		fmt.Printf("investigation %s %s\n", args[1], status)
		return nil
	default:
		return fmt.Errorf("unknown investigation subcommand %q", args[0])
	}
}

func integrationCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 2 {
		return errors.New("usage: osb integration <list|set|pull> <project> [args]")
	}
	sub, projectID := args[0], args[1]
	switch sub {
	case "list":
		pi, err := c.ListProjectIntegrations(ctx, projectID)
		if err != nil {
			return err
		}
		for _, cfg := range pi.Configs {
			fmt.Printf("%-12s %-40s key=%s cred=%s\n", cfg.Integration, cfg.BaseURL, cfg.ProjectKey, cfg.Credential)
		}
		modes := make([]string, 0, len(pi.Connectors))
		for _, conn := range pi.Connectors {
			m := "push"
			if conn.Pullable {
				m = "push+pull"
			}
			modes = append(modes, conn.Name+"("+m+")")
		}
		fmt.Printf("available: %s\n", strings.Join(modes, ", "))
		return nil
	case "set":
		fs := flag.NewFlagSet("integration set", flag.ContinueOnError)
		integration := fs.String("integration", "", "connector name (jira|defectdojo)")
		baseURL := fs.String("base-url", "", "tracker base URL")
		projectKey := fs.String("project-key", "", "tracker project key / test id")
		credential := fs.String("credential", "", "vault secret name holding the API token")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if *integration == "" || *baseURL == "" {
			return errors.New("integration set: --integration and --base-url are required")
		}
		cfg, err := c.SetIntegrationConfig(ctx, projectID, *integration, *baseURL, *projectKey, *credential)
		if err != nil {
			return err
		}
		fmt.Printf("configured %s for project %s\n", cfg.Integration, projectID)
		return nil
	case "pull":
		if len(args) < 3 {
			return errors.New("usage: osb integration pull <project> <integration>")
		}
		res, err := c.PullIntegration(ctx, projectID, args[2])
		if err != nil {
			return err
		}
		fmt.Printf("pulled %s: %d imported, %d already present (of %d)\n", args[2], res.Imported, res.Skipped, res.Total)
		return nil
	default:
		return fmt.Errorf("unknown integration subcommand %q", sub)
	}
}

func runnerCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb runner <list|enroll-token|rm>")
	}
	switch args[0] {
	case "list":
		rs, err := c.ListRunners(ctx)
		if err != nil {
			return err
		}
		if len(rs) == 0 {
			fmt.Println("no runners enrolled")
			return nil
		}
		for _, r := range rs {
			status := "offline"
			if r.Online {
				status = "online"
			}
			fmt.Printf("%-36s %-16s %-8s %s\n", r.ID, r.Name, r.Status, status)
		}
		return nil
	case "enroll-token":
		fs := flag.NewFlagSet("runner enroll-token", flag.ContinueOnError)
		label := fs.String("label", "", "label for the runner this token enrolls")
		ttl := fs.Int("ttl", 60, "token lifetime in minutes")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		tok, err := c.MintRunnerEnrollToken(ctx, *label, *ttl)
		if err != nil {
			return err
		}
		fmt.Printf("enrollment token (valid until %s):\n\n  %s\n\nStart the runner with:\n  osb-runner --url <control-plane-runner-addr> --enroll %s --name <name>\n",
			tok.ExpiresAt.Format("2006-01-02 15:04 MST"), tok.Token, tok.Token)
		return nil
	case "rm":
		if len(args) < 2 {
			return errors.New("usage: osb runner rm <id>")
		}
		if err := c.DeleteRunner(ctx, args[1]); err != nil {
			return err
		}
		fmt.Printf("runner %s revoked\n", args[1])
		return nil
	default:
		return fmt.Errorf("unknown runner subcommand %q", args[0])
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

func replayCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb replay <send|list|get|evidence>")
	}
	switch args[0] {
	case "send":
		fs := flag.NewFlagSet("replay send", flag.ContinueOnError)
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
			return errors.New("replay send: --project and --url are required")
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
		fs := flag.NewFlagSet("replay list", flag.ContinueOnError)
		project := fs.String("project", "", "project id (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *project == "" {
			return errors.New("replay list: --project is required")
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
			return errors.New("usage: osb replay get <id>")
		}
		ex, err := c.GetExchange(ctx, args[1])
		if err != nil {
			return err
		}
		return printJSON(ex)
	case "evidence":
		fs := flag.NewFlagSet("replay evidence", flag.ContinueOnError)
		id := fs.String("id", "", "exchange id (required)")
		note := fs.String("note", "", "note to attach to the observation")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *id == "" {
			return errors.New("replay evidence: --id is required")
		}
		obs, err := c.SaveExchangeEvidence(ctx, *id, *note)
		if err != nil {
			return err
		}
		return printJSON(obs)
	default:
		return fmt.Errorf("unknown replay subcommand %q", args[0])
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

func policyCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb policy <list|active|set>")
	}
	switch args[0] {
	case "list":
		ps, err := c.ListPolicyProfiles(ctx)
		if err != nil {
			return err
		}
		for _, p := range ps {
			fmt.Printf("%-10s ext-private=%-5v agent-private=%-5v  %s\n", p.Name, p.AllowExternalForPrivate, p.AgentSeesPrivate, p.Description)
		}
		return nil
	case "active":
		p, err := c.GetActivePolicy(ctx)
		if err != nil {
			return err
		}
		return printJSON(p)
	case "set":
		if len(args) < 2 {
			return errors.New("usage: osb policy set <personal|corporate|strict>")
		}
		p, err := c.SetActivePolicy(ctx, args[1])
		if err != nil {
			return err
		}
		fmt.Printf("active policy: %s\n", p.Name)
		return nil
	default:
		return fmt.Errorf("unknown policy subcommand %q", args[0])
	}
}

// hubCmd browses and installs from a community hub, and publishes to a local hub directory.
func hubCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb hub <browse|install|publish>")
	}
	switch args[0] {
	case "browse":
		fs := flag.NewFlagSet("hub browse", flag.ContinueOnError)
		url := fs.String("url", "", "hub base URL (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *url == "" {
			return errors.New("hub browse: --url is required")
		}
		pkgs, err := c.HubIndex(ctx, *url)
		if err != nil {
			return err
		}
		for _, p := range pkgs {
			fmt.Printf("%-28s v%-8s %-12s %v\n    %s\n", p.ID, p.Version, p.Publisher, p.Tags, p.Description)
		}
		return nil
	case "install":
		fs := flag.NewFlagSet("hub install", flag.ContinueOnError)
		url := fs.String("url", "", "hub base URL (required)")
		id := fs.String("id", "", "package id (required)")
		trust := fs.Bool("trust", false, "trust the publisher's key from the index (explicit consent)")
		allowUnsigned := fs.Bool("allow-unsigned", false, "install without a trusted signature (local dev)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *url == "" || *id == "" {
			return errors.New("hub install: --url and --id are required")
		}
		info, err := c.HubInstall(ctx, *url, *id, *trust, *allowUnsigned)
		if err != nil {
			return err
		}
		fmt.Printf("installed %s v%s (publisher %q, trusted=%v)\n", info.ID, info.Version, info.Publisher, info.Trusted)
		return nil
	case "publish":
		fs := flag.NewFlagSet("hub publish", flag.ContinueOnError)
		hubDir := fs.String("hub", "", "local hub directory to publish into (required)")
		dir := fs.String("dir", "", "package directory (required)")
		key := fs.String("key", "", "publisher public key file (.pub) to record in the index")
		desc := fs.String("description", "", "package description")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *hubDir == "" || *dir == "" {
			return errors.New("hub publish: --hub and --dir are required")
		}
		pub := ""
		if *key != "" {
			b, err := os.ReadFile(*key)
			if err != nil {
				return err
			}
			pub = strings.TrimSpace(string(b))
		}
		entry, err := hub.Publish(*hubDir, *dir, pub, *desc, nil)
		if err != nil {
			return err
		}
		fmt.Printf("published %s v%s to %s (digest %s)\n", entry.ID, entry.Version, *hubDir, entry.Digest[:12])
		return nil
	default:
		return fmt.Errorf("unknown hub subcommand %q", args[0])
	}
}

// extCmd manages extension packages: list loaded (via API), and author/sign locally.
func extCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb ext <list|keygen|sign>")
	}
	switch args[0] {
	case "list":
		exts, err := c.ListExtensions(ctx)
		if err != nil {
			return err
		}
		for _, e := range exts {
			trust := "untrusted"
			if e.Trusted {
				trust = "trusted"
			}
			fmt.Printf("%-28s v%-8s %-10s [%s] caps=%v\n", e.ID, e.Version, e.Publisher, trust, e.Capabilities)
		}
		return nil
	case "keygen":
		fs := flag.NewFlagSet("ext keygen", flag.ContinueOnError)
		out := fs.String("out", "publisher", "output key file prefix (writes <prefix>.pub / <prefix>.key)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		pub, priv, err := extension.GenerateKeyPair()
		if err != nil {
			return err
		}
		if err := os.WriteFile(*out+".pub", []byte(pub), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(*out+".key", []byte(priv), 0o600); err != nil {
			return err
		}
		fmt.Printf("wrote %s.pub (share to be trusted) and %s.key (keep secret)\n", *out, *out)
		return nil
	case "sign":
		fs := flag.NewFlagSet("ext sign", flag.ContinueOnError)
		dir := fs.String("dir", "", "package directory containing extension.json (required)")
		keyFile := fs.String("key", "", "private key file from keygen (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *dir == "" || *keyFile == "" {
			return errors.New("ext sign: --dir and --key are required")
		}
		raw, err := os.ReadFile(filepath.Join(*dir, extension.ManifestFile))
		if err != nil {
			return err
		}
		var m extension.Manifest
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		key, err := os.ReadFile(*keyFile)
		if err != nil {
			return err
		}
		sig, err := extension.Sign(m, strings.TrimSpace(string(key)))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(*dir, extension.SignatureFile), []byte(sig), 0o644); err != nil {
			return err
		}
		fmt.Printf("signed %s (publisher %q)\n", *dir, m.Publisher)
		return nil
	default:
		return fmt.Errorf("unknown ext subcommand %q", args[0])
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

	chromeArgs := browser.ProxyArgs(profile, st.Port, st.CASPKI, *startURL)
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

func canaryCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb canary <list|add|rm>")
	}
	switch args[0] {
	case "list":
		cs, err := c.ListCanaries(ctx)
		if err != nil {
			return err
		}
		for _, cn := range cs {
			fmt.Printf("%s  %-16s %s\n", cn.ID[:8], cn.Label, cn.Token)
		}
		return nil
	case "add":
		fs := flag.NewFlagSet("canary add", flag.ContinueOnError)
		label := fs.String("label", "", "canary label (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *label == "" {
			return errors.New("canary add: --label is required")
		}
		cn, err := c.CreateCanary(ctx, *label)
		if err != nil {
			return err
		}
		fmt.Printf("planted canary %q — place this token where exfil would grab it:\n%s\n", cn.Label, cn.Token)
		return nil
	case "rm":
		fs := flag.NewFlagSet("canary rm", flag.ContinueOnError)
		id := fs.String("id", "", "canary id (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *id == "" {
			return errors.New("canary rm: --id is required")
		}
		return c.DeleteCanary(ctx, *id)
	default:
		return fmt.Errorf("unknown canary subcommand %q", args[0])
	}
}

func dlpCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 || args[0] != "events" {
		return errors.New("usage: osb dlp events [--limit N]")
	}
	fs := flag.NewFlagSet("dlp events", flag.ContinueOnError)
	limit := fs.Int("limit", 50, "max events")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	events, err := c.ListDLPEvents(ctx, *limit)
	if err != nil {
		return err
	}
	for _, e := range events {
		mark := "alert"
		if e.Blocked {
			mark = "BLOCKED"
		}
		fmt.Printf("%s  %-8s %-8s %-10s %s\n", e.CreatedAt.Format("2006-01-02 15:04"), mark, e.Kind, e.Label, e.Location)
	}
	return nil
}

func secretCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb secret <set|list|rm>")
	}
	switch args[0] {
	case "set":
		fs := flag.NewFlagSet("secret set", flag.ContinueOnError)
		name := fs.String("name", "", "secret name (required)")
		value := fs.String("value", "", "secret value (omit to read from stdin — avoids shell history)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *name == "" {
			return errors.New("secret set: --name is required")
		}
		val := *value
		if val == "" {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			val = strings.TrimRight(string(data), "\n")
		}
		if val == "" {
			return errors.New("secret set: no value provided (use --value or pipe via stdin)")
		}
		meta, err := c.SetSecret(ctx, *name, val)
		if err != nil {
			return err
		}
		fmt.Printf("stored secret %q\n", meta.Name)
		return nil
	case "list":
		secrets, err := c.ListSecrets(ctx)
		if err != nil {
			return err
		}
		for _, s := range secrets {
			fmt.Printf("%s  (updated %s)\n", s.Name, s.UpdatedAt.Format("2006-01-02 15:04"))
		}
		return nil
	case "rm":
		fs := flag.NewFlagSet("secret rm", flag.ContinueOnError)
		name := fs.String("name", "", "secret name (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *name == "" {
			return errors.New("secret rm: --name is required")
		}
		return c.DeleteSecret(ctx, *name)
	default:
		return fmt.Errorf("unknown secret subcommand %q", args[0])
	}
}

func kbCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb kb <list|add|review|verify>")
	}
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("kb list", flag.ContinueOnError)
		project := fs.String("project", "", "project id (inherited KB)")
		target := fs.String("target", "", "target id (target KB)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		var entries []model.KBEntry
		var err error
		switch {
		case *target != "":
			entries, err = c.ListTargetKB(ctx, *target)
		case *project != "":
			entries, err = c.ListProjectKB(ctx, *project)
		default:
			return errors.New("kb list: --project or --target is required")
		}
		if err != nil {
			return err
		}
		for _, e := range entries {
			fmt.Printf("%s  %-12s [%-10s] %s\n", e.ID[:8], e.Kind, e.ReviewState, e.Title)
		}
		return nil
	case "add":
		fs := flag.NewFlagSet("kb add", flag.ContinueOnError)
		target := fs.String("target", "", "target id (required)")
		kind := fs.String("kind", "", "architecture|auth|endpoint|tech_stack|environment|data_flow|convention|gotcha|tactic (required)")
		title := fs.String("title", "", "entry title (required)")
		body := fs.String("body", "", "entry body")
		tags := fs.String("tags", "", "comma-separated tags")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *target == "" || *kind == "" || *title == "" {
			return errors.New("kb add: --target, --kind, and --title are required")
		}
		e, err := c.CreateKBEntry(ctx, *target, client.NewKBEntry{Kind: *kind, Title: *title, Body: *body, Tags: *tags})
		if err != nil {
			return err
		}
		return printJSON(e)
	case "review":
		fs := flag.NewFlagSet("kb review", flag.ContinueOnError)
		id := fs.String("id", "", "entry id (required)")
		state := fs.String("state", "", "confirmed|rejected|unreviewed (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *id == "" || *state == "" {
			return errors.New("kb review: --id and --state are required")
		}
		e, err := c.ReviewKBEntry(ctx, *id, *state)
		if err != nil {
			return err
		}
		return printJSON(e)
	case "verify":
		fs := flag.NewFlagSet("kb verify", flag.ContinueOnError)
		id := fs.String("id", "", "entry id (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *id == "" {
			return errors.New("kb verify: --id is required")
		}
		e, err := c.VerifyKBEntry(ctx, *id)
		if err != nil {
			return err
		}
		return printJSON(e)
	default:
		return fmt.Errorf("unknown kb subcommand %q", args[0])
	}
}

func methodologyCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb methodology <catalog|coverage|adopt|set|suggest>")
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
	case "suggest":
		fs := flag.NewFlagSet("methodology suggest", flag.ContinueOnError)
		project := fs.String("project", "", "project id (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *project == "" {
			return errors.New("methodology suggest: --project is required")
		}
		sugg, err := c.MethodologySuggestions(ctx, *project)
		if err != nil {
			return err
		}
		if len(sugg) == 0 {
			fmt.Println("no suggestions (adopt packs manually, or add knowledge-base entries)")
		}
		for _, s := range sugg {
			fmt.Printf("%-14s %s — %s\n", s.MethodologyID, s.Title, s.Reason)
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
	verify := fs.Bool("verify", false, "verify the hash-chain integrity (tamper detection)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *verify {
		v, err := c.VerifyAudit(ctx)
		if err != nil {
			return err
		}
		if v.OK {
			fmt.Printf("audit chain intact — %d events verified\n", v.Events)
			return nil
		}
		return fmt.Errorf("AUDIT CHAIN BROKEN at seq %d (tampering detected)", v.BrokenAt)
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
		taskID := fs.String("task", "", "task id (scope to one task's output)")
		project := fs.String("project", "", "project id (all the project's observations)")
		unreviewed := fs.Bool("unreviewed", false, "only observations still awaiting triage")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *taskID == "" && *project == "" {
			return errors.New("observation list: --task or --project is required")
		}
		var obs []model.Observation
		var err error
		if *project != "" {
			obs, err = c.ListProjectObservations(ctx, *project, *unreviewed)
		} else {
			obs, err = c.ListTaskObservations(ctx, *taskID)
		}
		if err != nil {
			return err
		}
		for _, o := range obs {
			sig := ""
			for _, k := range []string{"reachable", "exposed_route", "security_severity"} {
				if v := o.Attributes[k]; v != "" {
					sig += " " + k + "=" + v
				}
			}
			fmt.Printf("%s  [%s/%s] %-8s %s  %s%s\n", o.ID, o.Origin, o.ReviewState, o.Severity, o.Title, o.Location, sig)
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
		return errors.New("usage: osb finding <list|get|create|push|links>")
	}
	switch args[0] {
	case "push":
		fs := flag.NewFlagSet("finding push", flag.ContinueOnError)
		id := fs.String("id", "", "finding id (required)")
		integ := fs.String("integration", "", "integration: jira | defectdojo (required)")
		baseURL := fs.String("url", "", "base URL of the tracker (required)")
		projectKey := fs.String("project", "", "jira project key / defectdojo test id")
		cred := fs.String("credential", "", "vault secret NAME holding the auth token")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *id == "" || *integ == "" || *baseURL == "" {
			return errors.New("finding push: --id, --integration, and --url are required")
		}
		link, err := c.PushFinding(ctx, *id, client.PushFindingRequest{
			Integration: *integ, BaseURL: *baseURL, ProjectKey: *projectKey, Credential: *cred,
		})
		if err != nil {
			return err
		}
		fmt.Printf("linked to %s %s (%s)\n", link.Integration, link.ExternalID, link.ExternalURL)
		return nil
	case "links":
		if len(args) < 2 {
			return errors.New("usage: osb finding links <id>")
		}
		links, err := c.ListFindingLinks(ctx, args[1])
		if err != nil {
			return err
		}
		for _, l := range links {
			fmt.Printf("%-10s %-12s %s\n", l.Integration, l.ExternalID, l.ExternalURL)
		}
		return nil
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

// bundlePassphrase resolves the bundle passphrase from the flag or OSB_BUNDLE_PASSPHRASE.
func bundlePassphrase(flag string) string {
	if flag != "" {
		return flag
	}
	return os.Getenv("OSB_BUNDLE_PASSPHRASE")
}

func projectCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb project <list|create|get|delete|export|import>")
	}
	switch args[0] {
	case "export":
		fs := flag.NewFlagSet("project export", flag.ContinueOnError)
		id := fs.String("id", "", "project id (required)")
		out := fs.String("out", "", "output bundle file (required)")
		pass := fs.String("passphrase", "", "encryption passphrase (or set OSB_BUNDLE_PASSPHRASE)")
		signKey := fs.String("sign-key", "", "private key file to sign the bundle (from 'osb ext keygen')")
		publisher := fs.String("publisher", "", "publisher name recorded in the signature")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		phrase := bundlePassphrase(*pass)
		if *id == "" || *out == "" || phrase == "" {
			return errors.New("project export: --id, --out, and a passphrase are required")
		}
		data, err := c.ExportProject(ctx, *id, phrase)
		if err != nil {
			return err
		}
		if err := os.WriteFile(*out, data, 0o600); err != nil {
			return err
		}
		fmt.Printf("exported %d bytes to %s\n", len(data), *out)
		if *signKey != "" {
			key, err := os.ReadFile(*signKey)
			if err != nil {
				return err
			}
			sc, err := bundle.Sign(data, *publisher, strings.TrimSpace(string(key)))
			if err != nil {
				return err
			}
			raw, _ := bundle.MarshalSidecar(sc)
			if err := os.WriteFile(*out+".sig", raw, 0o600); err != nil {
				return err
			}
			fmt.Printf("signed → %s.sig (publisher %q)\n", *out, *publisher)
		}
		return nil
	case "import":
		fs := flag.NewFlagSet("project import", flag.ContinueOnError)
		file := fs.String("file", "", "bundle file (required)")
		pass := fs.String("passphrase", "", "decryption passphrase (or set OSB_BUNDLE_PASSPHRASE)")
		verify := fs.Bool("verify", false, "verify the bundle signature (<file>.sig) before importing")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		phrase := bundlePassphrase(*pass)
		if *file == "" || phrase == "" {
			return errors.New("project import: --file and a passphrase are required")
		}
		data, err := os.ReadFile(*file)
		if err != nil {
			return err
		}
		if *verify {
			sigRaw, err := os.ReadFile(*file + ".sig")
			if err != nil {
				return fmt.Errorf("verify: %w", err)
			}
			sc, err := bundle.ParseSidecar(sigRaw)
			if err != nil {
				return err
			}
			if err := sc.Verify(data); err != nil {
				return err
			}
			fmt.Printf("signature OK — publisher %q, key %s…\n", sc.Publisher, sc.PublicKey[:16])
		}
		newID, err := c.ImportBundle(ctx, data, phrase)
		if err != nil {
			return err
		}
		fmt.Printf("imported as project %s\n", newID)
		return nil
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
  project export --id ID --out FILE [--passphrase P]  encrypted project bundle
  project import --file FILE [--passphrase P]          import a bundle (new project)
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
  replay send --project ID --url URL [--method M] [--header 'K: v'] [--body B]  send an HTTP request
  replay list --project ID  list HTTP exchanges
  replay get <id>           show an exchange (request + response)
  replay evidence --id ID [--note N]  save a response as evidence (observation)
  session open --project ID   open a sandboxed terminal (attach in the desktop app)
  session list --project ID   list terminal sessions
  session close <id>          close a session and capture its transcript
  session evidence --id ID [--note N]  save a session transcript as evidence
  secret set --name X [--value Y]   store a secret (stdin if --value omitted)
  secret list                 list secret names
  secret rm --name X          delete a secret
  canary add --label X        plant a canary token (exfil tripwire)
  canary list                 list canaries
  dlp events [--limit N]      show DLP egress events
  kb list (--project ID | --target ID)  list knowledge-base entries
  kb add --target ID --kind K --title T [--body B] [--tags t]  add a KB entry
  kb review --id ID --state confirmed|rejected  curate an agent-drafted entry
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
  hub browse --url URL        browse a community hub
  hub install --url URL --id ID [--trust]  install a package from a hub
  hub publish --hub DIR --dir PKG [--key K.pub]  publish to a local hub
  policy list / set NAME      view / switch governance profile (personal|corporate|strict)
  ext list                    list loaded extension packages
  ext keygen --out NAME       generate a publisher key pair
  ext sign --dir DIR --key K  sign a package
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
  finding push --id ID --integration jira|defectdojo --url URL [--project K] [--credential SECRET]
  finding links <id>          list a finding's external tracker links
  analyst ask <message>       ask the Analyst (needs OSB_LLM_PROVIDER on the daemon)
  thread list                 list Analyst threads
  thread show <id>            show a thread's messages
  thread send <id> <message>  continue a thread
  approval list               list pending approvals
  approval approve <id>       approve a gated action and resume
  approval deny <id>          deny a gated action and resume
`)
}

func ragCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb rag <reindex|search>")
	}
	switch args[0] {
	case "reindex":
		fs := flag.NewFlagSet("rag reindex", flag.ContinueOnError)
		project := fs.String("project", "", "project id (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *project == "" {
			return errors.New("rag reindex: --project is required")
		}
		n, err := c.ReindexCorpus(ctx, *project)
		if err != nil {
			return err
		}
		fmt.Printf("reindexed: %d chunks\n", n)
		return nil
	case "search":
		fs := flag.NewFlagSet("rag search", flag.ContinueOnError)
		project := fs.String("project", "", "project id (required)")
		q := fs.String("q", "", "query text (required)")
		k := fs.Int("k", 5, "number of passages")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *project == "" || *q == "" {
			return errors.New("rag search: --project and --q are required")
		}
		hits, err := c.SearchCorpus(ctx, *project, *q, *k)
		if err != nil {
			return err
		}
		for _, h := range hits {
			text := h.Text
			if len(text) > 200 {
				text = text[:200] + "…"
			}
			fmt.Printf("[%.3f] %s/%s (%s)\n    %s\n", h.Score, h.SourceKind, h.SourceName, h.SourceID, text)
		}
		return nil
	default:
		return fmt.Errorf("unknown rag subcommand %q", args[0])
	}
}

func dossierCmd(ctx context.Context, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("dossier", flag.ContinueOnError)
	target := fs.String("target", "", "target id")
	project := fs.String("project", "", "project id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	kind, id := "targets", *target
	if *project != "" {
		kind, id = "projects", *project
	}
	if id == "" {
		return errors.New("usage: osb dossier --target <id> | --project <id>")
	}
	md, err := c.Dossier(ctx, kind, id)
	if err != nil {
		return err
	}
	fmt.Print(md)
	return nil
}

func planCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: osb plan <start|get|list|approve|deny>")
	}
	switch args[0] {
	case "start":
		fs := flag.NewFlagSet("plan start", flag.ContinueOnError)
		project := fs.String("project", "", "project id (required)")
		playbook := fs.String("playbook", "", "playbook id (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *project == "" || *playbook == "" {
			return errors.New("plan start: --project and --playbook are required")
		}
		p, err := c.StartPlan(ctx, *project, *playbook)
		if err != nil {
			return err
		}
		return printJSON(p)
	case "get":
		fs := flag.NewFlagSet("plan get", flag.ContinueOnError)
		id := fs.String("id", "", "plan id (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *id == "" {
			return errors.New("plan get: --id is required")
		}
		p, err := c.GetPlan(ctx, *id)
		if err != nil {
			return err
		}
		return printJSON(p)
	case "list":
		fs := flag.NewFlagSet("plan list", flag.ContinueOnError)
		project := fs.String("project", "", "project id (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *project == "" {
			return errors.New("plan list: --project is required")
		}
		plans, err := c.ListPlans(ctx, *project)
		if err != nil {
			return err
		}
		for _, p := range plans {
			fmt.Printf("%s  [%-8s] %s\n", p.ID[:8], p.Status, p.Goal)
		}
		return nil
	case "approve", "deny":
		fs := flag.NewFlagSet("plan "+args[0], flag.ContinueOnError)
		id := fs.String("id", "", "plan id (required)")
		step := fs.String("step", "", "gate step id (required)")
		note := fs.String("note", "", "optional note")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *id == "" || *step == "" {
			return errors.New("plan " + args[0] + ": --id and --step are required")
		}
		p, err := c.ResolvePlanGate(ctx, *id, *step, args[0] == "approve", *note)
		if err != nil {
			return err
		}
		return printJSON(p)
	default:
		return fmt.Errorf("unknown plan subcommand %q", args[0])
	}
}
