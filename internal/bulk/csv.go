// Package bulk reads the CSV files of a bulk import. It only parses: each
// row is then checked by the same validators as a link created by hand, so
// an import can't create a link the wizard would refuse.
package bulk

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Limits from API.md.
const (
	MaxBytes = 1 << 20 // 1 MB
	MaxRows  = 1000
)

// Codes for values that can't even be parsed. Everything else is reported
// with the link validators' codes.
const (
	CodeExpiryInvalid = "EXPIRY_INVALID"
	CodeCapInvalid    = "CAP_INVALID"
)

// Errors that refuse the whole file.
var (
	ErrNoHeader      = errors.New("the file is empty; the first row must be a header such as destination,alias,title")
	ErrNoDestination = errors.New(`the header row has no "destination" column`)
	ErrTooManyRows   = fmt.Errorf("the file has more than %d rows", MaxRows)
)

// Row is one data row. Line is its line in the file, the header being 1.
type Row struct {
	Line        int
	Destination string
	Alias       string
	Title       string
	Campaign    string
	Tags        []string
	ExpiresAt   *time.Time
	MaxClicks   *int64
	// Errors holds codes for values that could not be parsed.
	Errors []string
}

var columns = map[string]bool{
	"destination": true, "alias": true, "title": true, "campaign": true, "tags": true, "expires_at": true, "max_clicks": true,
}

// Parse reads a CSV with a header row. Unknown columns are ignored and
// blank rows skipped.
func Parse(r io.Reader) ([]Row, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // short rows just leave the trailing columns empty
	cr.TrimLeadingSpace = true

	header, err := cr.Read()
	if errors.Is(err, io.EOF) {
		return nil, ErrNoHeader
	}
	if err != nil {
		return nil, fmt.Errorf("the file is not valid CSV: %w", err)
	}
	index := map[string]int{}
	for i, h := range header {
		h = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))) // Excel adds a BOM
		if columns[h] {
			if _, dup := index[h]; !dup {
				index[h] = i
			}
		}
	}
	if _, ok := index["destination"]; !ok {
		return nil, ErrNoDestination
	}

	var rows []Row
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("the file is not valid CSV: %w", err)
		}
		if blank(rec) {
			continue
		}
		if len(rows) == MaxRows {
			return nil, ErrTooManyRows
		}
		line, _ := cr.FieldPos(0)
		get := func(col string) string {
			if i, ok := index[col]; ok && i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		row := Row{Line: line, Destination: get("destination"), Alias: get("alias"), Title: get("title"), Campaign: get("campaign")}
		row.Tags = splitTags(get("tags"))
		if v := get("expires_at"); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				row.Errors = append(row.Errors, CodeExpiryInvalid)
			} else {
				row.ExpiresAt = &t
			}
		}
		if v := get("max_clicks"); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				row.Errors = append(row.Errors, CodeCapInvalid)
			} else {
				row.MaxClicks = &n
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func blank(rec []string) bool {
	for _, f := range rec {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}

// splitTags splits "a; b;A" into distinct names, ignoring case.
func splitTags(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range strings.Split(s, ";") {
		t = strings.TrimSpace(t)
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	return out
}
