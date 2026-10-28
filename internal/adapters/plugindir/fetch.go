package plugindir

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
	fetchFor       = 5 * time.Minute // to fetch an archive, the whole of it
	maxRedirects   = 3
	fetchUserAgent = "Polythought-Plugins/1"
)

var _ ports.ArchiveFetcher = (*Fetcher)(nil)

// Fetcher fetches the archives admins install from a URL: over https only, through the
// guard that keeps out the server's own network unless an admin allows it (as the
// webhooks' does), redirects too.
type Fetcher struct {
	client *http.Client
}

// NewFetcher makes a fetcher; proxy names the outbound proxy, empty or nil for none
// (D428), and tlsConfig, when given, is for tests that serve their own certificate.
func NewFetcher(allowPrivate func() bool, proxy func() string, tlsConfig *tls.Config) *Fetcher {
	dialer := webhook.GuardedDialer(allowPrivate, proxy)

	return &Fetcher{client: &http.Client{
		Transport: &http.Transport{
			Proxy:               nil, // the dialer goes through one, not the transport, which would hide the address
			DialContext:         dialer.DialContext,
			TLSClientConfig:     tlsConfig.Clone(), // the transport adds to it: never the caller's
			ForceAttemptHTTP2:   true,
			TLSHandshakeTimeout: 10 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			switch {
			case len(via) >= maxRedirects:
				return fmt.Errorf("more than %d redirects", maxRedirects)
			case req.URL.Scheme != "https":
				return errors.New("redirected away from https")
			}

			return nil
		},
	}}
}

// Fetch fetches the archive at rawURL, as Install reads it: past max bytes, it says so.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string, max int64) (io.ReadCloser, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return nil, fmt.Errorf("%w: an archive's URL is an https address", domain.ErrInvalidInput)
	}

	ctx, cancel := context.WithTimeout(ctx, fetchFor)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err)
	}

	req.Header.Set("User-Agent", fetchUserAgent)

	resp, err := f.client.Do(req)
	if err != nil {
		cancel()

		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err
		}

		return nil, fmt.Errorf("%w: fetch from %s: %v", domain.ErrInvalidInput, u.Hostname(), err)
	}

	switch {
	case resp.StatusCode != http.StatusOK:
		_ = resp.Body.Close()
		cancel()

		return nil, fmt.Errorf("%w: %s answered %d", domain.ErrInvalidInput, u.Hostname(), resp.StatusCode)
	case resp.ContentLength > max:
		_ = resp.Body.Close()
		cancel()

		return nil, fmt.Errorf("%w: the archive is larger than the %d bytes an archive may be", domain.ErrInvalidInput, max)
	}

	return &fetched{Reader: io.LimitReader(resp.Body, max+1), body: resp.Body, cancel: cancel}, nil
}

// fetched is a response's body, read at most a byte past the cap, so Install can tell.
type fetched struct {
	io.Reader
	body   io.Closer
	cancel context.CancelFunc
}

func (f *fetched) Close() error {
	defer f.cancel()
	return f.body.Close()
}
