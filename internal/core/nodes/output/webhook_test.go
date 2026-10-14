package output_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/credtypes"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/output"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

const hook = "https://discord.com/api/webhooks/1/token"

func items(n int) engine.Ports {
	in := engine.Ports{}
	for i := range n {
		in.Add(engine.DefaultPort, engine.Item{JSON: map[string]any{
			"question": fmt.Sprintf("Q%d?", i), "spread": decimal.RequireFromString("0.01"),
		}})
	}

	return in
}

func TestWebhook(t *testing.T) {
	tests := []struct {
		name       string
		params     map[string]any
		in         engine.Ports
		postErr    error
		wantBodies []string
		wantPassed int
		wantFailed int
	}{
		{
			name:       "one message listing every item",
			params:     map[string]any{"url": hook, "message": "{{question}} spread {{spread}}"},
			in:         items(2),
			wantBodies: []string{`{"content":"Q0? spread 0.01\nQ1? spread 0.01","text":"Q0? spread 0.01\nQ1? spread 0.01"}`},
			wantPassed: 2,
		},
		{
			name:       "a message per item",
			params:     map[string]any{"url": hook, "message": "{{question}}", "send": "each"},
			in:         items(2),
			wantBodies: []string{`{"content":"Q0?","text":"Q0?"}`, `{"content":"Q1?","text":"Q1?"}`},
			wantPassed: 2,
		},
		{
			name:       "the items as JSON",
			params:     map[string]any{"url": hook, "format": "json"},
			in:         items(1),
			wantBodies: []string{`{"items":[{"question":"Q0?","spread":"0.01"}]}`},
			wantPassed: 1,
		},
		{
			name:       "each item as JSON",
			params:     map[string]any{"url": hook, "format": "json", "send": "each"},
			in:         items(1),
			wantBodies: []string{`{"question":"Q0?","spread":"0.01"}`},
			wantPassed: 1,
		},
		{
			name:       "a refused request fails its items",
			params:     map[string]any{"url": hook},
			in:         items(2),
			postErr:    errors.New("webhook to discord.com answered 404"),
			wantBodies: []string{`{"content":"Update from your Polythought workflow\nUpdate from your Polythought workflow","text":"Update from your Polythought workflow\nUpdate from your Polythought workflow"}`},
			wantFailed: 2,
		},
		{
			name:       "no more than twenty requests a run",
			params:     map[string]any{"url": hook, "send": "each"},
			in:         items(22),
			wantPassed: 20,
			wantFailed: 2,
		},
		{name: "nothing to send", params: map[string]any{"url": hook}, in: engine.Ports{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var bodies []string

			sender := portsmocks.NewMockWebhookSender(t)
			sender.EXPECT().Post(mock.Anything, hook, mock.Anything, map[string]string(nil)).
				RunAndReturn(func(_ context.Context, _ string, body []byte, _ map[string]string) error {
					bodies = append(bodies, string(body))
					return tt.postErr
				}).Maybe()

			node := output.NewWebhook(sender, nil)
			out, err := node.Run(context.Background(), engine.RunContext{Params: engine.LiteralParams(node.Type(), tt.params)}, tt.in)
			require.NoError(t, err)

			if tt.wantBodies != nil {
				assert.Equal(t, len(tt.wantBodies), len(bodies))
				for i, want := range tt.wantBodies {
					assert.JSONEq(t, want, bodies[i])
				}
			}

			assert.Len(t, out[engine.DefaultPort], tt.wantPassed)
			assert.Len(t, out[engine.ErrorPort], tt.wantFailed)
		})
	}
}

func TestWebhookCredential(t *testing.T) {
	const owner = domain.UserID("u-1")

	run := func(t *testing.T, node *output.Webhook, params map[string]any) (engine.Ports, error) {
		t.Helper()

		rc := engine.RunContext{OwnerID: owner, Params: engine.LiteralParams(node.Type(), params)}

		return node.Run(context.Background(), rc, items(1))
	}

	t.Run("adds the credential's header", func(t *testing.T) {
		sender := portsmocks.NewMockWebhookSender(t)
		sender.EXPECT().Post(mock.Anything, hook, mock.Anything, map[string]string{"Authorization": "Bearer t-1"}).Return(nil).Once()

		creds := portsmocks.NewMockCredentialReader(t)
		creds.EXPECT().Values(mock.Anything, owner, domain.CredentialID("c-1"), credtypes.HTTPHeader).
			Return(map[string]string{"header": "Authorization", "value": "Bearer t-1"}, nil).Once()

		out, err := run(t, output.NewWebhook(sender, creds), map[string]any{"url": hook, "credential": "c-1"})
		require.NoError(t, err)
		assert.Len(t, out[engine.DefaultPort], 1)
	})

	t.Run("a credential that is not the owner's fails the node", func(t *testing.T) {
		creds := portsmocks.NewMockCredentialReader(t)
		creds.EXPECT().Values(mock.Anything, owner, domain.CredentialID("c-9"), credtypes.HTTPHeader).Return(nil, domain.ErrNotFound)

		_, err := run(t, output.NewWebhook(portsmocks.NewMockWebhookSender(t), creds), map[string]any{"url": hook, "credential": "c-9"})
		require.ErrorIs(t, err, domain.ErrNotFound)
		assert.ErrorContains(t, err, "credential: not found")
	})

	t.Run("without a vault, a credential cannot be used", func(t *testing.T) {
		_, err := run(t, output.NewWebhook(portsmocks.NewMockWebhookSender(t), nil), map[string]any{"url": hook, "credential": "c-1"})
		require.ErrorContains(t, err, "credentials are not available here")
	})

	t.Run("the param asks for a header credential and is personal", func(t *testing.T) {
		var spec engine.ParamSpec
		for _, p := range output.NewWebhook(nil, nil).Type().Params {
			if p.Name == "credential" {
				spec = p
			}
		}

		assert.Equal(t, engine.ParamCredential, spec.Kind)
		assert.Equal(t, credtypes.HTTPHeader, spec.CredentialType)
		assert.False(t, spec.Required)
		assert.True(t, spec.Personal())
	})
}
