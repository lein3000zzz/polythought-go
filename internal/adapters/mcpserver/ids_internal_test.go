package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Requests renumbered in a batch, answers to the server's own requests left alone, and the
// callers' IDs put back on a stream and on JSON (D432).
func TestRenumber(t *testing.T) {
	r := newRequestIDs()

	body := `[{"jsonrpc":"2.0","id":0,"method":"tools/call"},{"jsonrpc":"2.0","id":"x","method":"ping"},` +
		`{"jsonrpc":"2.0","id":3,"result":{}},{"jsonrpc":"2.0","method":"notifications/progress"}]`

	out, ours, ok := r.renumber("s-1", []byte(body))
	require.True(t, ok)
	require.Len(t, ours, 2)

	var msgs []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &msgs))
	assert.Equal(t, `"pt-1"`, string(msgs[0]["id"]))
	assert.Equal(t, `"pt-2"`, string(msgs[1]["id"]))
	assert.Equal(t, `3`, string(msgs[2]["id"]), "the answer to the server's request")
	assert.NotContains(t, msgs[3], "id")

	_, _, ok = r.renumber("s-1", []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	assert.False(t, ok, "nothing to renumber: as it came")

	_, _, ok = r.renumber("s-1", []byte(`not json`))
	assert.False(t, ok)

	t.Run("on a stream", func(t *testing.T) {
		rec := httptest.NewRecorder()
		rec.Header().Set("Content-Type", "text/event-stream")
		a := &answers{ResponseWriter: rec, ours: ours}
		a.WriteHeader(http.StatusOK)

		_, _ = a.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":\"pt-1\",\"result\":{}}\n\nevent: message\n"))
		_, _ = a.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"id\":7,\"method\":\"elicitation/create\"}\n\n"))
		a.finish()

		assert.Equal(t, "event: message\ndata: {\"id\":0,\"jsonrpc\":\"2.0\",\"result\":{}}\n\n"+
			"event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":7,\"method\":\"elicitation/create\"}\n\n", rec.Body.String())
	})

	t.Run("as JSON", func(t *testing.T) {
		rec := httptest.NewRecorder()
		rec.Header().Set("Content-Type", "application/json")
		rec.Header().Set("Content-Length", "40")
		a := &answers{ResponseWriter: rec, ours: ours}
		a.WriteHeader(http.StatusBadRequest)

		_, _ = a.Write([]byte(`{"jsonrpc":"2.0","id":"pt-2","error":{"code":-32600,"message":"no"}}`))
		a.finish()

		assert.JSONEq(t, `{"jsonrpc":"2.0","id":"x","error":{"code":-32600,"message":"no"}}`, rec.Body.String())
		assert.Empty(t, rec.Header().Get("Content-Length"))
	})

	r.answered("s-1", ours)
	assert.Empty(t, r.inFlight, "forgotten once answered")
}
