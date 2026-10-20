package bulk

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	in := "\ufeffDestination, Alias ,title,tags,expires_at,max_clicks,extra\n" +
		"https://a.example,promo,Promo,a; b;A,2026-11-05T18:29:59Z,10,x\n" +
		"\n" +
		"https://b.example\n" +
		`"https://c.example/?q=a,b",,"Quoted, title",,tomorrow,ten` + "\n"
	rows, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("%d rows, want 3 (blank rows skipped)", len(rows))
	}
	r := rows[0]
	if r.Line != 2 || r.Destination != "https://a.example" || r.Alias != "promo" || r.Title != "Promo" ||
		!slices.Equal(r.Tags, []string{"a", "b"}) || r.ExpiresAt == nil || *r.MaxClicks != 10 || len(r.Errors) != 0 {
		t.Fatalf("row 1 = %+v", r)
	}
	if rows[1].Line != 4 || rows[1].Destination != "https://b.example" || rows[1].Alias != "" {
		t.Fatalf("short row = %+v", rows[1])
	}
	if rows[2].Destination != "https://c.example/?q=a,b" || rows[2].Title != "Quoted, title" ||
		!slices.Equal(rows[2].Errors, []string{CodeExpiryInvalid, CodeCapInvalid}) {
		t.Fatalf("row 3 = %+v", rows[2])
	}
}

func TestParseRefusesFile(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want error
	}{
		{"empty", "", ErrNoHeader},
		{"no destination column", "alias,title\nx,y\n", ErrNoDestination},
		{"too many rows", "destination\n" + strings.Repeat("https://a.example\n", MaxRows+1), ErrTooManyRows},
	}
	for _, tt := range tests {
		if _, err := Parse(strings.NewReader(tt.in)); !errors.Is(err, tt.want) {
			t.Errorf("%s: %v, want %v", tt.name, err, tt.want)
		}
	}
	if rows, err := Parse(strings.NewReader("destination\n" + strings.Repeat("https://a.example\n", MaxRows))); err != nil || len(rows) != MaxRows {
		t.Errorf("exactly %d rows: %d, %v", MaxRows, len(rows), err)
	}
}
