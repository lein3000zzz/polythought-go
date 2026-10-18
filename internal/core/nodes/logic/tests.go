package logic

import (
	"fmt"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/fields"
)

// Tests (D118, D125 to D128). If and Filter test each item with Field, Test and Value,
// and with each row of More tests; Match says how the results count together. Every test
// is checked, so a field one cannot test fails the item whatever the others say.

const (
	MatchAll  = "all"
	MatchAny  = "any"
	MatchNone = "none"
	MatchOne  = "one"
)

// testNames lists the tests in the order an editor offers them. The first six compare
// numbers; is and isNot compare text whole, in any case (D131).
var testNames = []string{"gt", "gte", "lt", "lte", "eq", "neq", "is", "isNot", "contains", "notContains", "exists", "missing"}

var matchNames = []string{MatchAll, MatchAny, MatchNone, MatchOne}

// operators maps a comparison's name to the results of Cmp that satisfy it.
var operators = map[string]func(cmp int) bool{
	"gt":  func(c int) bool { return c > 0 },
	"gte": func(c int) bool { return c >= 0 },
	"lt":  func(c int) bool { return c < 0 },
	"lte": func(c int) bool { return c <= 0 },
	"eq":  func(c int) bool { return c == 0 },
	"neq": func(c int) bool { return c != 0 },
}

const valueDescription = "A number for the comparisons, text for is and contains (any case). Not used by exists and missing."

// moreTestColumns are a row of More tests: a test like the first.
var moreTestColumns = []engine.ParamSpec{
	{Name: "field", Label: "Field", Kind: engine.ParamString, Required: true, Description: "The item field to test."},
	{Name: "operator", Label: "Test", Kind: engine.ParamString, Default: "gt", Options: testNames},
	{Name: "value", Label: "Value", Kind: engine.ParamString, Numbers: true, Description: valueDescription},
}

// testParams are the settings of a node that tests items.
func testParams() []engine.ParamSpec {
	return []engine.ParamSpec{
		{
			Name: "field", Label: "Field", Kind: engine.ParamString, Required: true, Field: true,
			Description: "The item field to test, e.g. spread, or outcomes.0.price for the first outcome's price.",
		},
		{Name: "operator", Label: "Test", Kind: engine.ParamString, Default: "gt", Options: testNames},
		{Name: "value", Label: "Value", Kind: engine.ParamString, Numbers: true, Description: valueDescription},
		{
			Name: "tests", Label: "More tests", Kind: engine.ParamTable, Columns: moreTestColumns,
			Description: "More tests like the first, a row each. Match says how they count together.",
		},
		{
			Name: "match", Label: "Match", Kind: engine.ParamString, Default: MatchAll, Options: matchNames,
			Description: "all: every test passes (AND). any: one or more do (OR). none: not one does (NOT). " +
				"one: exactly one does (XOR).",
		},
	}
}

// test is one test, and where it is set, for errors: opAt names its test, valueAt its
// value.
type test struct {
	field, op, value string
	opAt, valueAt    string
}

// passes reports whether item passes the tests in p, as Match counts them. The first
// error is the item's, for the error port; the second, for bad settings, fails the node.
func passes(p engine.Params, item engine.Item) (bool, error, error) {
	tests, err := testsOf(p)
	if err != nil {
		return false, nil, err
	}

	match, err := p.String("match")
	if err != nil {
		return false, nil, err
	}

	passed := 0

	for _, t := range tests {
		ok, itemErr, err := t.check(item)
		if err != nil || itemErr != nil {
			return false, itemErr, err
		}

		if ok {
			passed++
		}
	}

	switch match {
	case MatchAll:
		return passed == len(tests), nil, nil
	case MatchAny:
		return passed > 0, nil, nil
	case MatchNone:
		return passed == 0, nil, nil
	case MatchOne:
		return passed == 1, nil, nil
	default:
		return false, nil, fmt.Errorf("%w %q: unknown match %q", engine.ErrInvalidParam, "match", match)
	}
}

// testsOf reads the tests: Field, Test and Value, as worked out for the item, then More
// tests.
func testsOf(p engine.Params) ([]test, error) {
	first := test{opAt: `"operator"`, valueAt: `"value"`}

	var err error

	if first.field, err = p.String("field"); err != nil {
		return nil, err
	}

	if first.op, err = p.String("operator"); err != nil {
		return nil, err
	}

	first.value, _ = p.Text("value") // optional for exists and missing

	rows, err := p.Table("tests", moreTestColumns)
	if err != nil {
		return nil, err
	}

	out := []test{first}

	for i, row := range rows {
		t, err := rowTest(row, fmt.Sprintf(`"tests": in row %d`, i+1))
		if err != nil {
			return nil, err
		}

		out = append(out, t)
	}

	return out, nil
}

// rowTest reads a test from a table row of field, operator and value; at says where.
func rowTest(row engine.Params, at string) (test, error) {
	t := test{opAt: at, valueAt: at}

	var err error

	if t.field, err = row.String("field"); err != nil {
		return test{}, err
	}

	if t.op, err = row.String("operator"); err != nil {
		return test{}, err
	}

	t.value, _ = row.Text("value") // optional for exists and missing

	return t, nil
}

// valid says what is wrong with t's settings: a test that does not exist, or a value a
// comparison cannot read.
func (t test) valid() error {
	switch t.op {
	case "exists", "missing", "is", "isNot", "contains", "notContains":
		return nil
	}

	if _, known := operators[t.op]; !known {
		return fmt.Errorf("%w %s: unknown test %q", engine.ErrInvalidParam, t.opAt, t.op)
	}

	if _, ok := fields.Number(t.value); !ok {
		return fmt.Errorf("%w %s: %q is not a number, which %s compares with", engine.ErrInvalidParam, t.valueAt, t.value, t.op)
	}

	return nil
}

// check reports whether item passes t. The first error is the item's; the second, for
// bad settings, fails the node.
func (t test) check(item engine.Item) (bool, error, error) {
	if err := t.valid(); err != nil {
		return false, nil, err
	}

	got, present := fields.Lookup(item.JSON, t.field)
	present = present && got != nil

	switch t.op {
	case "exists":
		return present, nil, nil
	case "missing":
		return !present, nil, nil
	case "is", "isNot":
		same := present && strings.EqualFold(fields.Format(got), t.value)
		return same == (t.op == "is"), nil, nil
	case "contains", "notContains":
		has := present && strings.Contains(strings.ToLower(fields.Format(got)), strings.ToLower(t.value))
		return has == (t.op == "contains"), nil, nil
	}

	satisfies := operators[t.op]
	want, _ := fields.Number(t.value)

	if !present {
		return false, nil, nil // nothing to compare, like a one-sided book's spread
	}

	n, ok := fields.Number(got)
	if !ok {
		return false, fmt.Errorf("field %q is %q, not a number", t.field, fields.Format(got)), nil
	}

	return satisfies(n.Cmp(want)), nil, nil
}
