package engine

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

var address = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// CheckValue says why value does not fit the setting, as saving checks it, or nil.
func (spec ParamSpec) CheckValue(value any) error {
	if msg := checkParam(spec, value); msg != "" {
		return errors.New(msg)
	}

	return nil
}

// checkParam validates a configured value against its spec before anything runs, so a
// mistyped value is reported when the workflow is saved rather than when it executes.
// It returns a description of the problem, or "" when the value is fine.
func checkParam(spec ParamSpec, value any) string {
	p := Params{spec.Name: value}

	switch spec.Kind {
	case ParamString, ParamWallet, ParamCredential, ParamCode:
		read, want := p.String, "must be a string"
		if spec.Numbers && spec.Kind == ParamString {
			read, want = p.Text, "must be text or a number"
		}

		s, err := read(spec.Name)
		if err != nil {
			return want
		}

		return checkOption(spec, s)

	case ParamDuration:
		s, err := p.String(spec.Name)
		if err != nil {
			return "must be a duration like 30s"
		}

		if d, err := time.ParseDuration(s); err != nil || d <= 0 {
			return fmt.Sprintf("%q is not a positive duration like 30s", s)
		}

	case ParamNumber:
		if _, err := p.Int(spec.Name); err != nil {
			return "must be a whole number"
		}

	case ParamDecimal:
		if _, err := p.Decimal(spec.Name); err != nil {
			return "must be a number"
		}

	case ParamBool:
		if _, err := p.Bool(spec.Name); err != nil {
			return "must be true or false"
		}

	case ParamList, ParamTokens:
		list, err := p.Strings(spec.Name)
		if err != nil {
			return "must be a list of strings"
		}

		for _, s := range list {
			if msg := checkOption(spec, s); msg != "" {
				return msg
			}
		}

	case ParamAddress:
		s, err := p.String(spec.Name)
		if err != nil {
			return "must be an address"
		}

		if !address.MatchString(s) {
			return fmt.Sprintf("%q is not an address: 0x and 40 hex digits", s)
		}

		if _, err := domain.ParseAddress(s); err != nil {
			return fmt.Sprintf("%q has a typo: its mixed case does not match its checksum", s)
		}

	case ParamTable:
		return checkTable(spec, value)
	}

	return ""
}

// checkTableParams checks a node type's tables when it registers: columns only on tables,
// and of the kinds cells hold (D80), each named once.
func checkTableParams(typ NodeType) error {
	for _, p := range typ.Params {
		if p.Kind != ParamTable {
			if len(p.Columns) > 0 {
				return fmt.Errorf("register node type %q: param %q has columns but is not a table", typ.Name, p.Name)
			}

			continue
		}

		if len(p.Columns) == 0 {
			return fmt.Errorf("register node type %q: table %q has no columns", typ.Name, p.Name)
		}

		seen := map[string]bool{}

		for _, c := range p.Columns {
			switch {
			case c.Name == "" || seen[c.Name]:
				return fmt.Errorf("register node type %q: table %q: column %q is unnamed or named twice", typ.Name, p.Name, c.Name)
			case !slices.Contains(CellKinds, c.Kind):
				return fmt.Errorf("register node type %q: table %q: column %q cannot be %s", typ.Name, p.Name, c.Name, c.Kind)
			case len(c.Options) > 0 && c.Kind != ParamString:
				return fmt.Errorf("register node type %q: table %q: only text columns have choices, not %q", typ.Name, p.Name, c.Name)
			}

			seen[c.Name] = true
		}
	}

	return nil
}

// checkTable checks every row of a table param: each cell as its column's kind, required
// ones there, no others.
func checkTable(spec ParamSpec, value any) string {
	rows, ok := value.([]any)
	if !ok {
		return "must be a table: a list of rows"
	}

	if len(rows) > MaxRows {
		return fmt.Sprintf("has %d rows, more than the %d allowed", len(rows), MaxRows)
	}

	for i, r := range rows {
		row, ok := r.(map[string]any)
		if !ok {
			return fmt.Sprintf("in row %d: must be a row of cells", i+1)
		}

		for _, col := range spec.Columns {
			cell := row[col.Name]
			if cell == nil {
				if col.Required && col.Default == nil {
					return fmt.Sprintf("in row %d: %s is missing", i+1, columnName(col))
				}

				continue
			}

			if msg := checkParam(col, cell); msg != "" {
				return fmt.Sprintf("in row %d: %s %s", i+1, columnName(col), msg)
			}
		}

		for _, key := range slices.Sorted(maps.Keys(row)) {
			if !slices.ContainsFunc(spec.Columns, func(c ParamSpec) bool { return c.Name == key }) {
				return fmt.Sprintf("in row %d: there is no column %q", i+1, key)
			}
		}
	}

	return ""
}

func columnName(col ParamSpec) string {
	if col.Label != "" {
		return col.Label
	}

	return col.Name
}

func checkOption(spec ParamSpec, s string) string {
	if len(spec.Options) == 0 || slices.Contains(spec.Options, s) {
		return ""
	}

	return fmt.Sprintf("%q is not one of %v", s, spec.Options)
}
