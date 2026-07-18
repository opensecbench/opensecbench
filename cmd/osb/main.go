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
	case "project":
		return projectCmd(ctx, c, args[1:])
	case "capability", "cap":
		return capabilityCmd(ctx, c, args[1:])
	case "task":
		return taskCmd(ctx, c, args[1:])
	case "artifact":
		return artifactCmd(ctx, c, args[1:])
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
		out, err := c.RunTask(ctx, client.RunTaskRequest{
			CapabilityID: *id,
			TargetDir:    *dir,
			Actor:        *actor,
			Params:       p,
		})
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
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *name == "" {
			return errors.New("project create: --name is required")
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
  project list                list projects
  project get <id>            show a project
  project create --name NAME  create a project
  project delete <id>         delete a project
  capability list             list available capabilities
  capability run --id ID --dir PATH [--param k=v]  run a capability
  task get <id>               show a task
  artifact get <id>           write an artifact's bytes to stdout
`)
}
