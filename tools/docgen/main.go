// Command docgen writes the reference pages of the docs site from the code: node types,
// settings, credential types and the types for code, and the generated declarations of
// @polythought/sdk next to the SDK. Run with `make docs-gen`; a test fails when the
// files on disk are stale.
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
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/config"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

func main() {
	dir := flag.String("dir", "docs/src/content/docs/reference", "the reference directory of the docs site")
	typesDir := flag.String("types", typesDir, "where the SDK's declarations go")
	flag.Parse()

	pages, types, err := build()
	if err != nil {
		log.Fatal(err)
	}

	if err := write(*dir, pages); err != nil {
		log.Fatal(err)
	}

	for name, content := range types {
		if err := os.WriteFile(filepath.Join(*typesDir, name), []byte(content), 0o644); err != nil { //nolint:gosec // public
			log.Fatal(err)
		}
	}

	example, err := exampleFile()
	if err != nil {
		log.Fatal(err)
	}

	if err := os.WriteFile(exampleConfigPath, []byte(example), 0o644); err != nil { //nolint:gosec // public
		log.Fatal(err)
	}
}

// exampleFile is the example config file, of the bootstrap values and the settings the
// server reads.
func exampleFile() (string, error) {
	specs, err := config.Settings()
	if err != nil {
		return "", err
	}

	return exampleConfig(config.Bootstrap, specs.Specs()), nil
}

// typesDir is where the SDK's declarations are, from the repository's root.
const typesDir = "internal/adapters/tscompiler/sdk/types"

// build generates the pages and the declarations from the node types and settings the
// server registers.
func build() (Pages, map[string]string, error) {
	registry := engine.NewRegistry()

	// Only the types are read, so the nodes get no real dependencies; the market and
	// wallet triggers register only with a feed, and the Telegram nodes only with the bot.
	err := nodes.Register(registry, nodes.Deps{
		Feed: noFeed{}, Traders: noTraders{}, Messenger: noTelegram{}, TelegramInbox: noTelegram{}, Asker: noTelegram{}, Code: noCode{},
		Logger: logging.Noop(),
	})
	if err != nil {
		return nil, nil, err
	}

	specs, err := config.Settings()
	if err != nil {
		return nil, nil, err
	}

	pages, err := Generate(registry.Types(), registry.CredentialTypes(), specs.Specs())
	if err != nil {
		return nil, nil, err
	}

	return pages, TypeFiles(registry.Types()), nil
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

type noTraders struct{}

func (noTraders) Follow(context.Context, []domain.WalletAddress, time.Time) (ports.TraderSubscription, error) {
	return nil, nil //nolint:nilnil // never called: only the node types are read
}

type noTelegram struct{}

func (noTelegram) MessageUser(context.Context, domain.UserID, ports.UserMessage) error { return nil }

func (noTelegram) AskUser(context.Context, domain.UserID, ports.Question) (int, error) { return 0, nil }

func (noTelegram) CloseQuestion(context.Context, domain.UserID, int, string) error { return nil }

func (noTelegram) Subscribe(domain.UserID, func(string) bool) (<-chan ports.TelegramInboxMessage, func()) {
	return nil, func() {}
}

type noCode struct{}

func (noCode) Call(context.Context, ports.CodeCall) (ports.CodeResult, error) {
	return ports.CodeResult{}, nil
}
