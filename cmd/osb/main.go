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
	"path/filepath"
	"strings"

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
	case "observation", "obs":
		return observationCmd(ctx, c, args[1:])
	case "finding":
		return findingCmd(ctx, c, args[1:])
	case "analyst":
		return analystCmd(ctx, c, args[1:])
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
		out, err := c.RunTask(ctx, req)
		if err != nil {
			return err
		}
		return printJSON(out)
	default:
		return fmt.Errorf("unknown capability subcommand %q", args[0])
	}
}

func taskCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 2 || args[0] != "get" {
		return errors.New("usage: osb task get <id>")
	}
	t, err := c.GetTask(ctx, args[1])
	if err != nil {
		return err
	}
	return printJSON(t)
}

func analystCmd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 2 || args[0] != "ask" {
		return errors.New("usage: osb analyst ask <message>")
	}
	res, err := c.AnalystAsk(ctx, strings.Join(args[1:], " "))
	if err != nil {
		return err
	}
	for _, s := range res.Steps {
		status := "ok"
		if !s.Approved {
			status = "denied"
		} else if s.Error != "" {
			status = "error: " + s.Error
		}
		fmt.Printf("  · %s [%s]\n", s.Call.Tool, status)
	}
	fmt.Println(res.Answer)
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
  capability list             list available capabilities
  capability run --id ID (--dir PATH | --asset ID) [--param k=v]  run a capability
  task get <id>               show a task
  artifact get <id>           write an artifact's bytes to stdout
  observation list --task ID  list a task's observations
  observation review <id> --state confirmed|rejected
  finding create --title T [--severity S] [--cwe C] [--obs ID ...]
  finding list                list findings
  finding get <id>            show a finding
  analyst ask <message>       ask the Analyst (needs OSB_LLM_PROVIDER on the daemon)
`)
}
