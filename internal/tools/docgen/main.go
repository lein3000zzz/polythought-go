// Command docgen writes the reference pages of the docs site from the code: node types,
// settings and credential types. Run with `make docs-gen`; a test fails when the pages
// on disk are stale.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/config"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

func main() {
	dir := flag.String("dir", "docs/src/content/docs/reference", "the reference directory of the docs site")
	flag.Parse()

	pages, err := build()
	if err != nil {
		log.Fatal(err)
	}

	if err := write(*dir, pages); err != nil {
		log.Fatal(err)
	}
}

// build generates the pages from the node types and settings the server registers.
func build() (Pages, error) {
	registry := engine.NewRegistry()

	// Only the types are read, so the nodes get no real dependencies; the market trigger
	// registers only with a feed, and the Telegram nodes only with the bot.
	err := nodes.Register(registry, nodes.Deps{
		Feed: noFeed{}, Messenger: noTelegram{}, TelegramInbox: noTelegram{}, Asker: noTelegram{}, Logger: logging.Noop(),
	})
	if err != nil {
		return nil, err
	}

	specs, err := config.Settings()
	if err != nil {
		return nil, err
	}

	return Generate(registry.Types(), registry.CredentialTypes(), specs.Specs())
}

// write puts the pages under dir and removes generated pages that are no longer made.
func write(dir string, pages Pages) error {
	stale, err := generatedFiles(dir)
	if err != nil {
		return err
	}

	for path, content := range pages {
		full := filepath.Join(dir, path)
		delete(stale, full)

		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}

		if err := os.WriteFile(full, []byte(content), 0o644); err != nil { //nolint:gosec // docs are public
			return err
		}
	}

	for path := range stale {
		if err := os.Remove(path); err != nil {
			return err
		}
	}

	return nil
}

// generatedFiles lists the pages under dir that docgen wrote, by their marker.
func generatedFiles(dir string) (map[string]bool, error) {
	out := make(map[string]bool)

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}

			return err
		}

		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		if bytes.Contains(content, []byte(marker)) {
			out[path] = true
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("docgen: %w", err)
	}

	return out, nil
}

type noFeed struct{}

func (noFeed) Subscribe(context.Context, []domain.TokenID) (ports.FeedSubscription, error) {
	return nil, nil //nolint:nilnil // never called: only the node types are read
}

type noTelegram struct{}

func (noTelegram) MessageUser(context.Context, domain.UserID, ports.UserMessage) error { return nil }

func (noTelegram) AskUser(context.Context, domain.UserID, ports.Question) (int, error) { return 0, nil }

func (noTelegram) CloseQuestion(context.Context, domain.UserID, int, string) error { return nil }

func (noTelegram) Subscribe(domain.UserID, func(string) bool) (<-chan ports.TelegramInboxMessage, func()) {
	return nil, func() {}
}
