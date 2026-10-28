// Package clientdocs fetches OAuth clients' metadata documents (D397): a client whose ID is
// an https URL describes itself at that URL, and only its owner can publish there.
package clientdocs

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/adapters/webhook"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const (
	fetchFor  = 5 * time.Second
	maxBody   = 16 << 10 // a document names a client and its redirects: a few hundred bytes
	userAgent = "Polythought-OAuth/1"
)

var _ ports.ClientDocuments = (*Fetcher)(nil)

// Fetcher fetches over https only, through the guard that keeps out the server's own
// network, as webhooks' does; it follows no redirect, as the document must be at its ID.
type Fetcher struct {
	client *http.Client
}

// New makes a fetcher; proxy names the outbound proxy, empty or nil for none (D428), and
// tlsConfig, when given, is for tests that serve their own certificate.
func New(allowPrivate func() bool, proxy func() string, tlsConfig *tls.Config) *Fetcher {
	dialer := webhook.GuardedDialer(allowPrivate, proxy)

	return &Fetcher{client: &http.Client{
		Timeout: fetchFor,
		Transport: &http.Transport{
			Proxy:               nil, // the dialer goes through one, not the transport, which would hide the address
			DialContext:         dialer.DialContext,
			TLSClientConfig:     tlsConfig.Clone(),
			TLSHandshakeTimeout: fetchFor,
			// A document is fetched about once a day: no connection is kept for the next,
			// where a host that never closes one would keep it, and its memory, for good (A12).
			DisableKeepAlives:      true,
			MaxResponseHeaderBytes: maxBody,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("a client's document is at its ID: it redirects")
		},
	}}
}

func (f *Fetcher) Fetch(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return nil, fmt.Errorf("%w: a client's ID is an https URL", domain.ErrInvalidInput)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err)
	}

	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	res, err := f.client.Do(req)
	if err != nil {
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err
		}

		return nil, fmt.Errorf("%w: %s: %v", domain.ErrUnavailable, u.Hostname(), err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s answered %d", domain.ErrUnavailable, u.Hostname(), res.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", domain.ErrUnavailable, u.Hostname(), err)
	}

	if len(body) > maxBody {
		return nil, fmt.Errorf("%w: %s's document is larger than %d bytes", domain.ErrInvalidInput, u.Hostname(), maxBody)
	}

	return body, nil
}
