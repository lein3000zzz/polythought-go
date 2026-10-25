// Package models lists the models a user's language model key can use, for the agent
// node's picker: as the provider lists them, kept an hour (D251). Any other name can
// still be typed.
package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/credtypes"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/lru"
)

const (
	// kept is how long a list is kept.
	kept = time.Hour
	// keys is how many keys' lists are kept at once.
	keys = 1024
)

type Deps struct {
	Credentials ports.CredentialReader
	LLM         ports.LLM
	Now         func() time.Time // optional
}

type Service struct {
	credentials ports.CredentialReader
	llm         ports.LLM
	now         func() time.Time
	lists       *lru.Cache[string, list]
}

// list is a key's models, as listed at a time.
type list struct {
	models []ports.LLMModel
	at     time.Time
}

func New(d Deps) *Service {
	now := d.Now
	if now == nil {
		now = time.Now
	}

	return &Service{credentials: d.Credentials, llm: d.LLM, now: now, lists: lru.New[string, list](keys)}
}

// List is the models the key in one of p's credentials can use, from its provider at most
// once an hour. A list is kept for the key itself: a key or base URL changed since asks
// again. A failure is not kept, so trying again asks again.
func (s *Service) List(ctx context.Context, p auth.Principal, id domain.CredentialID) ([]ports.LLMModel, error) {
	// Listing sends the key to its provider, as a test does (A18, D404).
	if !p.MayUseCredentials() {
		return nil, fmt.Errorf("%w: listing a key's models sends it to its provider, which needs the user to let agents "+
			"and tokens use credentials: Settings, Connected agents", domain.ErrForbidden)
	}

	credType, values, err := s.credentials.Values(ctx, p.User.ID, id, credtypes.LLMKeyTypes)
	if err != nil {
		return nil, err
	}

	key, _ := credtypes.LLMKey(credType, values)
	name := listName(p.User.ID, id, key)

	if l, ok := s.lists.Get(name); ok && s.now().Sub(l.at) < kept {
		return l.models, nil
	}

	models, err := s.llm.Models(ctx, key)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		return nil, fmt.Errorf("%w: %w", domain.ErrProviderFailed, err)
	}

	if models == nil {
		models = []ports.LLMModel{}
	}

	s.lists.Put(name, list{models: models, at: s.now()})

	return models, nil
}

// listName names a key's list by its owner, credential and a hash of the key, so the
// cache holds no key and a changed one misses.
func listName(owner domain.UserID, id domain.CredentialID, key ports.LLMKey) string {
	h := sha256.Sum256([]byte(string(key.Provider) + "\x00" + key.APIKey + "\x00" + key.BaseURL))

	return string(owner) + "\x00" + string(id) + "\x00" + hex.EncodeToString(h[:])
}
