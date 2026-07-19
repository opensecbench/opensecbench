package store

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestListExchangesFiltered(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, NewProject{Name: "engagement"})

	mk := func(origin, method, url string, status int) {
		e, err := db.CreateExchange(ctx, model.HTTPExchange{ProjectID: proj.ID, Origin: origin, Method: method, URL: url})
		if err != nil {
			t.Fatal(err)
		}
		if status != 0 {
			if err := db.RecordResponse(ctx, e.ID, status, "", "", 5, ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	mk("proxy", "GET", "https://api.acme.com/users", 200)
	mk("proxy", "POST", "https://api.acme.com/login", 401)
	mk("replay", "GET", "https://other.example/x", 200)

	cases := []struct {
		name   string
		filter ExchangeFilter
		want   int
	}{
		{"all", ExchangeFilter{}, 3},
		{"origin proxy", ExchangeFilter{Origin: "proxy"}, 2},
		{"method POST", ExchangeFilter{Method: "POST"}, 1},
		{"status 401", ExchangeFilter{Status: 401}, 1},
		{"url substring", ExchangeFilter{Query: "acme.com"}, 2},
		{"wildcard is literal", ExchangeFilter{Query: "%"}, 0},
		{"combined", ExchangeFilter{Origin: "proxy", Method: "GET"}, 1},
		{"limit", ExchangeFilter{Limit: 1}, 1},
	}
	for _, c := range cases {
		got, err := db.ListExchangesFiltered(ctx, proj.ID, c.filter)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(got) != c.want {
			t.Fatalf("%s: got %d, want %d", c.name, len(got), c.want)
		}
	}
}
