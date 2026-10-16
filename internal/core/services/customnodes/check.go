package customnodes

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/customnodes"
)

// How big a custom node may be (D56).
const (
	MaxSettings = 20
	MaxOutputs  = 10
	MaxSource   = 64 << 10

	maxLabel       = 80
	maxDescription = 2000
)

var (
	// A node's name, and an output's: spread-split.
	namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	portPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,39}$`)
	// A setting's, and an item field's: what code reads as ctx.params.threshold or
	// item.bestBid.
	keyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,39}$`)
)

// settingKinds are the kinds a custom node's settings take: the built-ins' but
// credentials, so code never sees a secret (D54), and code itself.
var settingKinds = []engine.ParamKind{
	engine.ParamString, engine.ParamNumber, engine.ParamDecimal, engine.ParamBool,
	engine.ParamDuration, engine.ParamList, engine.ParamWallet,
}

// apply checks def and copies it onto n. Problems come back together, each with the part
// of the form it is about as its param: name, settings.0.default, outputs.1, source.
func (s *Service) apply(ctx context.Context, n *domain.CustomNode, def Definition) error {
	v := domain.ValidationError{Base: domain.ErrInvalidInput}
	add := func(param, format string, args ...any) {
		v.Add(domain.Problem{Param: param, Message: fmt.Sprintf(format, args...)})
	}

	if !namePattern.MatchString(def.Name) {
		add("name", "a name is lowercase letters, digits and hyphens, starting with a letter, up to 40")
	}

	label := strings.TrimSpace(def.Label)
	if label == "" || utf8.RuneCountInString(label) > maxLabel {
		add("label", "a label is 1 to %d characters", maxLabel)
	}

	description := strings.TrimSpace(def.Description)
	if utf8.RuneCountInString(description) > maxDescription {
		add("description", "a description is at most %d characters", maxDescription)
	}

	settings := checkSettings(def.Settings, add)
	checkOutputs(def.Outputs, add)
	checkItems(def.Items, add)

	if len(def.Source) > MaxSource {
		add("source", "the code is %d KiB, more than the %d KiB allowed", len(def.Source)>>10, MaxSource>>10)
	} else if err := s.compile(ctx, def.Source, add); err != nil {
		return err
	}

	if err := v.Err(); err != nil {
		return err
	}

	candidate := *n
	candidate.Name, candidate.Label, candidate.Description = def.Name, label, description
	candidate.Settings, candidate.Outputs, candidate.Items = settings, slices.Clone(def.Outputs), def.Items
	candidate.Source = def.Source

	// The checks above say what is wrong in the form's words; registering is the last
	// word on the type.
	if _, err := s.base.With(customnodes.New(candidate, nil, nil)); err != nil {
		return invalid("", err.Error())
	}

	*n = candidate

	return nil
}

// checkSettings reports what is wrong with settings, and gives them with a label each.
func checkSettings(settings []domain.CustomSetting, add func(param, format string, args ...any)) []domain.CustomSetting {
	if len(settings) > MaxSettings {
		add("settings", "at most %d settings", MaxSettings)
	}

	out := make([]domain.CustomSetting, len(settings))
	seen := map[string]bool{}

	for i, st := range settings {
		at := fmt.Sprintf("settings.%d", i)
		kind := engine.ParamKind(st.Kind)

		switch {
		case !keyPattern.MatchString(st.Name):
			add(at+".name", "a setting's name is letters, digits and underscores, not starting with a digit, up to 40")
		case seen[st.Name]:
			add(at+".name", "another setting is called %s", st.Name)
		}

		seen[st.Name] = true

		if !slices.Contains(settingKinds, kind) {
			add(at+".kind", "a setting is text, a number, a decimal, true or false, a duration, a list or a wallet, not %q", st.Kind)
		}

		textual := kind == engine.ParamString || kind == engine.ParamList

		if len(st.Options) > 0 && !textual {
			add(at+".options", "only text and lists have choices")
		}

		if st.Field && !textual {
			add(at+".field", "only text and lists name fields")
		}

		if len(st.FieldKinds) > 0 && !st.Field {
			add(at+".fieldKinds", "only a setting that names a field has field kinds")
		}

		for _, k := range st.FieldKinds {
			if engine.CheckFields([]engine.Field{fieldOfKind(k)}) != nil {
				add(at+".fieldKinds", "no field kind %q", k)
			}
		}

		if st.Default != nil {
			spec := customnodes.TypeOf(domain.CustomNode{Settings: []domain.CustomSetting{st}}).Params[0]

			switch {
			case kind == engine.ParamWallet:
				add(at+".default", "a wallet is whoever uses the node's, so it has no default")
			case slices.Contains(settingKinds, kind):
				if err := spec.CheckValue(st.Default); err != nil {
					add(at+".default", "the default %v", err)
				}
			}
		}

		out[i] = st
		out[i].Label = strings.TrimSpace(st.Label)
		out[i].Description = strings.TrimSpace(st.Description)

		if out[i].Label == "" {
			out[i].Label = st.Name
		}
	}

	return out
}

// fieldOfKind is a field of kind k, for asking whether k is one: lists and objects take
// what they hold.
func fieldOfKind(k string) engine.Field {
	f := engine.Field{Name: "f", Kind: engine.FieldKind(k)}

	switch f.Kind {
	case engine.FieldList:
		f.Of = &engine.Field{Kind: engine.FieldAny}
	case engine.FieldObject:
		f.Fields = []engine.Field{{Name: "g", Kind: engine.FieldAny}}
	}

	return f
}

func checkOutputs(outputs []string, add func(param, format string, args ...any)) {
	if len(outputs) == 0 || len(outputs) > MaxOutputs {
		add("outputs", "a node has 1 to %d outputs", MaxOutputs)
	}

	seen := map[string]bool{}

	for i, port := range outputs {
		at := fmt.Sprintf("outputs.%d", i)

		switch {
		case !portPattern.MatchString(port):
			add(at, "an output's name is lowercase letters, digits, hyphens and underscores, starting with a letter, up to 40")
		case port == engine.ErrorPort:
			add(at, "error is the port failed items go out of")
		case seen[port]:
			add(at, "another output is called %s", port)
		}

		seen[port] = true
	}
}

func checkItems(items domain.CustomItems, add func(param, format string, args ...any)) {
	if items.Passed {
		if len(items.Fields) > 0 {
			add("items", "a node passes the items on, or makes its own with fields, not both")
		}

		return
	}

	if err := fieldNames(items.Fields, ""); err != nil {
		add("items", "%v", err)
		return
	}

	if err := engine.CheckFields(customnodes.Fields(items.Fields)); err != nil {
		add("items", "%v", err)
	}
}

// fieldNames keeps item fields to names code can read as item.name.
func fieldNames(fields []domain.CustomField, path string) error {
	for _, f := range fields {
		if !keyPattern.MatchString(f.Name) {
			return fmt.Errorf("field %q: a field's name is letters, digits and underscores, not starting with a digit, up to 40", path+f.Name)
		}

		if err := fieldNames(f.Fields, path+f.Name+"."); err != nil {
			return err
		}

		if f.Of != nil {
			if err := fieldNames(f.Of.Fields, path+f.Name+"[]."); err != nil {
				return err
			}
		}
	}

	return nil
}

// compile reports where the code does not compile. Other errors are the compiler's.
func (s *Service) compile(ctx context.Context, source string, add func(param, format string, args ...any)) error {
	if strings.TrimSpace(source) == "" {
		add("source", "the code is empty: it needs export function run(items, ctx)")
		return nil
	}

	_, err := s.compiler.Compile(ctx, source)

	var codeErr *domain.CodeError
	if !errors.As(err, &codeErr) {
		return err
	}

	if len(codeErr.Problems) == 0 {
		add("source", "%s", codeErr.Message)
	}

	for _, p := range codeErr.Problems {
		add("source", "line %d, column %d: %s", p.Line, p.Column, p.Message)
	}

	return nil
}

func invalid(param, message string) error {
	return &domain.ValidationError{Base: domain.ErrInvalidInput, Problems: []domain.Problem{{Param: param, Message: message}}}
}
