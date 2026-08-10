package task

import (
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

const cdxFixture = `{
  "metadata": {"component": {"bom-ref": "root-app"}},
  "components": [
    {"bom-ref": "root-app", "name": "myapp", "purl": "pkg:maven/com.acme/myapp@1.0"},
    {"bom-ref": "ref-api", "name": "api", "purl": "pkg:maven/org.example/api@2.0"},
    {"bom-ref": "ref-util", "name": "util", "purl": "pkg:maven/org.example/util@3.0"},
    {"bom-ref": "ref-orphan", "name": "orphan", "purl": "pkg:maven/org.example/orphan@9.0"}
  ],
  "dependencies": [
    {"ref": "root-app", "dependsOn": ["ref-api"]},
    {"ref": "ref-api", "dependsOn": ["ref-util"]}
  ]
}`

func TestDepGraphClassifyAndPath(t *testing.T) {
	g := buildDepGraph([]byte(cdxFixture))
	if g == nil {
		t.Fatal("buildDepGraph returned nil")
		return
	}
	// Directly declared by the root.
	if g.class["org.example:api"] != "direct" {
		t.Fatalf("api class = %q, want direct", g.class["org.example:api"])
	}
	// One hop below a direct dep → transitive, with the chain recorded.
	if g.class["org.example:util"] != "transitive" {
		t.Fatalf("util class = %q, want transitive", g.class["org.example:util"])
	}
	// Present as a component but not linked from the root by any edge → transitive, no path.
	if g.class["org.example:orphan"] != "transitive" {
		t.Fatalf("orphan class = %q, want transitive", g.class["org.example:orphan"])
	}

	// correlateDependency sets the attributes; a transitive dep with a chain gets dependency_path.
	util := model.Observation{Attributes: map[string]string{"package": "org.example:util"}}
	correlateDependency(&util, g)
	if util.Attributes["dependency"] != "transitive" || util.Attributes["dependency_path"] != "org.example:api → org.example:util" {
		t.Fatalf("util correlated attrs = %+v", util.Attributes)
	}
	// A direct dep gets no dependency_path (it's just itself).
	api := model.Observation{Attributes: map[string]string{"package": "org.example:api"}}
	correlateDependency(&api, g)
	if api.Attributes["dependency"] != "direct" || api.Attributes["dependency_path"] != "" {
		t.Fatalf("api correlated attrs = %+v", api.Attributes)
	}
	// An already-classified observation is left untouched.
	pre := model.Observation{Attributes: map[string]string{"package": "org.example:api", "dependency": "manual"}}
	correlateDependency(&pre, g)
	if pre.Attributes["dependency"] != "manual" {
		t.Fatal("correlateDependency should not overwrite an existing dependency attr")
	}
}

func TestPurlCoordinate(t *testing.T) {
	cases := map[string]string{
		"pkg:maven/org.apache.commons/commons-text@1.9?type=jar": "org.apache.commons:commons-text",
		"pkg:golang/golang.org/x/net@v0.0.0":                     "golang.org/x/net",
		"pkg:npm/left-pad@1.0.0":                                 "left-pad",
		"not-a-purl":                                             "",
	}
	for in, want := range cases {
		if got := purlCoordinate(in); got != want {
			t.Errorf("purlCoordinate(%q) = %q, want %q", in, got, want)
		}
	}
}
