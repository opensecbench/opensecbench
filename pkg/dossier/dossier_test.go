package dossier

import (
	"strings"
	"testing"
	"time"

	"github.com/opensecbench/opensecbench/pkg/model"
)

var testNow = time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)

func TestAssembleGroupsAndOrders(t *testing.T) {
	entries := []model.KBEntry{
		{ID: "1", Kind: model.KBGotcha, Title: "admin console exposed", Scope: "target", ReviewState: "confirmed", LastVerifiedAt: testNow},
		{ID: "2", Kind: model.KBAuth, Title: "OIDC via Keycloak", Scope: "org", ReviewState: "confirmed", LastVerifiedAt: testNow},
		{ID: "3", Kind: model.KBAuth, Title: "app-specific role check", Scope: "target", ReviewState: "unreviewed"},
		{ID: "4", Kind: model.KBArchitecture, Title: "nginx + Go on Cloud Run", Scope: "target", ReviewState: "confirmed", LastVerifiedAt: testNow},
		{ID: "5", Kind: model.KBAuth, Title: "rejected note", Scope: "target", ReviewState: "rejected"},
	}
	d := Assemble("acme-web", entries, testNow)
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

// A confirmed fact aged past its kind's window is flagged stale, sorts after fresh confirmed entries, and is
// tagged in the markdown; a fresh one is not. A never-verified draft is not "stale" — it's a draft.
func TestStaleness(t *testing.T) {
	old := testNow.Add(-200 * 24 * time.Hour) // > auth's 180d window
	entries := []model.KBEntry{
		{ID: "fresh", Kind: model.KBAuth, Title: "fresh fact", Scope: "target", ReviewState: "confirmed", LastVerifiedAt: testNow},
		{ID: "stale", Kind: model.KBAuth, Title: "stale fact", Scope: "target", ReviewState: "confirmed", LastVerifiedAt: old},
		{ID: "draft", Kind: model.KBAuth, Title: "draft fact", Scope: "target", ReviewState: "unreviewed"},
	}
	d := Assemble("acme-web", entries, testNow)
	auth := d.Sections[0]
	// Order: fresh confirmed, then stale confirmed, then draft.
	if got := []string{auth.Entries[0].ID, auth.Entries[1].ID, auth.Entries[2].ID}; got[0] != "fresh" || got[1] != "stale" || got[2] != "draft" {
		t.Fatalf("status ordering wrong: %v", got)
	}
	if auth.Entries[0].Stale || !auth.Entries[1].Stale {
		t.Fatalf("staleness flags wrong: fresh=%v stale=%v", auth.Entries[0].Stale, auth.Entries[1].Stale)
	}
	// A never-verified draft must not be marked stale (that's a different state).
	if auth.Entries[2].Stale {
		t.Fatalf("draft should not be stale")
	}
	md := d.Markdown()
	if !strings.Contains(md, "stale — re-verify") {
		t.Fatalf("markdown should tag the stale entry:\n%s", md)
	}
}

func TestStaleAfterByKind(t *testing.T) {
	// Structural facts stay fresh far longer than concrete surface.
	if StaleAfter(model.KBArchitecture) <= StaleAfter(model.KBEndpoint) {
		t.Fatalf("architecture should outlast endpoint")
	}
	if StaleAfter("some_future_kind") != defaultStaleAfter {
		t.Fatalf("unknown kind should use the default window")
	}
	// Never-verified is never stale.
	if IsStale(model.KBEndpoint, time.Time{}, testNow) {
		t.Fatalf("zero last-verified must not be stale")
	}
}

func TestMarkdown(t *testing.T) {
	d := Assemble("acme-web", []model.KBEntry{
		{ID: "1", Kind: model.KBAuth, Title: "OIDC via Keycloak", Body: "Shared realm.", Scope: "org", ReviewState: "confirmed", LastVerifiedAt: testNow},
		{ID: "2", Kind: model.KBGotcha, Title: "admin console exposed", Scope: "target", ReviewState: "unreviewed"},
	}, testNow)
	md := d.Markdown()
	for _, want := range []string{"What we know: acme-web", "Authentication & authorization", "OIDC via Keycloak", "(org)", "Shared realm.", "Gotchas & pitfalls", "(draft)"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q:\n%s", want, md)
		}
	}

	empty := Assemble("x", nil, testNow).Markdown()
	if !strings.Contains(empty, "No knowledge captured yet") {
		t.Fatalf("empty dossier should say so: %s", empty)
	}
}
