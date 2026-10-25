package settings

import (
	"context"
	"fmt"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// An admin's agent's or token's change to Config, and its restarting the server, wait for
// the admin where they say so (D411); secret settings are the browser's alone.

// Confirmer holds an action for its user to confirm: the confirmations service.
type Confirmer interface {
	Hold(ctx context.Context, p auth.Principal, a domain.Ask) error
}

// UseConfirmations has an admin's agents' and tokens' work on Config wait for the admin.
// Call it once, as the server starts: the confirmations service comes after the settings.
func (s *Service) UseConfirmations(c Confirmer) {
	s.confirm = c
}

// RestartAsked is what a held restart carries out: nothing more.
type RestartAsked struct{}

// agentsMay refuses a token's or an agent's change to a secret setting, or to one that says
// where a secret goes (A7).
func (s *Service) agentsMay(p auth.Principal, keys []string) error {
	if p.Itself() {
		return nil
	}

	for _, key := range keys {
		spec, ok := s.registry.Spec(key)

		switch {
		case !ok:
		case spec.Secret:
			return fmt.Errorf("%w: %s is secret: an admin sets it at a browser, on the Config page", domain.ErrForbidden, spec.Label)
		case spec.AtBrowser:
			return fmt.Errorf("%w: %s says where a secret goes: an admin sets it at a browser, on the Config page",
				domain.ErrForbidden, spec.Label)
		}
	}

	return nil
}

func (s *Service) holdChanges(ctx context.Context, p auth.Principal, batch []ports.SettingChange, changes any) error {
	if s.confirm == nil || p.Itself() {
		return nil
	}

	said := make([]string, len(batch))

	for i, c := range batch {
		spec, _ := s.registry.Spec(c.Key)

		switch {
		case c.Reset:
			said[i] = spec.Label + " back to its default"
		default:
			said[i] = spec.Label + " to " + string(c.NewValue)
		}
	}

	return s.confirm.Hold(ctx, p, domain.Ask{Kind: domain.ConfirmConfig, What: "Change Config: " + strings.Join(said, "; "), Payload: changes})
}
