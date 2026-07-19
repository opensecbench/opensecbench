package dossier

import (
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestAssembleGroupsAndOrders(t *testing.T) {
	entries := []model.KBEntry{
		{ID: "1", Kind: model.KBGotcha, Title: "admin console exposed", Scope: "target", ReviewState: "confirmed"},
		{ID: "2", Kind: model.KBAuth, Title: "OIDC via Keycloak", Scope: "org", ReviewState: "confirmed"},
		{ID: "3", Kind: model.KBAuth, Title: "app-specific role check", Scope: "target", ReviewState: "unreviewed"},
		{ID: "4", Kind: model.KBArchitecture, Title: "nginx + Go on Cloud Run", Scope: "target", ReviewState: "confirmed"},
		{ID: "5", Kind: model.KBAuth, Title: "rejected note", Scope: "target", ReviewState: "rejected"},
	}
	d := Assemble("acme-web", entries)
	if d.Entries != 4 { // the rejected one is dropped
		t.Fatalf("entries = %d, want 4 (rejected dropped)", d.Entries)
	}
	// Architecture comes before auth (reading order); gotcha last.
	kinds := make([]string, len(d.Sections))
	for i, s := range d.Sections {
		kinds[i] = s.Kind
	}
	if kinds[0] != model.KBArchitecture || kinds[len(kinds)-1] != model.KBGotcha {
		t.Fatalf("section order wrong: %v", kinds)
	}
	// Within auth: confirmed before draft.
	var auth Section
	for _, s := range d.Sections {
		if s.Kind == model.KBAuth {
			auth = s
		}
	}
	if len(auth.Entries) != 2 || auth.Entries[0].ReviewState != "confirmed" || auth.Entries[1].ReviewState != "unreviewed" {
		t.Fatalf("auth ordering wrong: %+v", auth.Entries)
	}
}

func TestMarkdown(t *testing.T) {
	d := Assemble("acme-web", []model.KBEntry{
		{ID: "1", Kind: model.KBAuth, Title: "OIDC via Keycloak", Body: "Shared realm.", Scope: "org", ReviewState: "confirmed"},
		{ID: "2", Kind: model.KBGotcha, Title: "admin console exposed", Scope: "target", ReviewState: "unreviewed"},
	})
	md := d.Markdown()
	for _, want := range []string{"What we know: acme-web", "Authentication & authorization", "OIDC via Keycloak", "(org)", "Shared realm.", "Gotchas & pitfalls", "(draft)"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q:\n%s", want, md)
		}
	}

	empty := Assemble("x", nil).Markdown()
	if !strings.Contains(empty, "No knowledge captured yet") {
		t.Fatalf("empty dossier should say so: %s", empty)
	}
}
