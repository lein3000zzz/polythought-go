package engine

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

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
		s, err := p.String(spec.Name)
		if err != nil {
			return "must be a string"
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
	}

	return ""
}

func checkOption(spec ParamSpec, s string) string {
	if len(spec.Options) == 0 || slices.Contains(spec.Options, s) {
		return ""
	}

	return fmt.Sprintf("%q is not one of %v", s, spec.Options)
}
