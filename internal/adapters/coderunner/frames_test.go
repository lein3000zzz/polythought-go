package coderunner

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// A result of a million empty lines, and as many problems, is read without decoding them
// all: no more lines than a run keeps, the rest counted, and no problems (P10).
func TestBomb(t *testing.T) {
	const lines = 1 << 20

	raw := []byte(`{"kind":"result","id":1,"result":{"logs":[` + strings.Repeat(`{},`, lines-1) +
		`{}],"code":{"kind":"threw","Problems":[` + strings.Repeat(`{},`, lines-1) + `{}]}}}`)

	frame := binary.BigEndian.AppendUint32(nil, uint32(len(raw)))
	frame = append(frame, raw...)

	var before, after runtime.MemStats

	runtime.GC()
	runtime.ReadMemStats(&before)

	m, err := readFrame(bytes.NewReader(frame), maxFrame)

	runtime.ReadMemStats(&after)
	require.NoError(t, err)

	assert.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(3*len(raw)), "about the frame, not a line's worth each")

	require.NotNil(t, m.Result)
	assert.Len(t, m.Result.Logs.lines, maxLogs+1)
	assert.Equal(t, lines-maxLogs-1, m.Result.Logs.more)

	logs := held(m.Result.Logs)
	require.Len(t, logs, maxLogs+1)
	assert.Equal(t, ports.CodeLog{Level: "log"}, logs[0])
	assert.Equal(t, ports.CodeLog{Level: "info", Text: fmt.Sprintf("… and %d more lines, not kept", lines-maxLogs)}, logs[maxLogs])
}

// A sound runner's logs are kept as they are; a line past maxLogs is the engine's when
// it is the line saying how many more there were, and only then.
func TestHeld(t *testing.T) {
	sound := []ports.CodeLog{
		{Level: "warn", Text: strings.Repeat("x", maxLogBytes-1) + cutLog},
		{Level: "error", Text: strings.Repeat("y", maxLogBytes)},
	}
	for len(sound) < maxLogs {
		sound = append(sound, ports.CodeLog{Level: "log", Text: "z"})
	}

	assert.Equal(t, sound[:2], held(logs{lines: sound[:2]}))

	more := slices.Concat(sound, []ports.CodeLog{{Level: "info", Text: fmt.Sprintf(droppedLogs, 7)}})
	assert.Equal(t, more, held(logs{lines: more}))

	forged := slices.Concat(sound, []ports.CodeLog{{Level: "info", Text: fmt.Sprintf(droppedLogs, 7) + " and then some"}})
	assert.Equal(t, slices.Concat(sound, []ports.CodeLog{{Level: "info", Text: fmt.Sprintf(droppedLogs, 1)}}), held(logs{lines: forged}))

	assert.Nil(t, held(logs{}))
}

// Lines come through a frame as they were sent, whatever their text holds.
func TestLogsSent(t *testing.T) {
	sent := []ports.CodeLog{
		{Level: "log", Text: `a, b] c} {"d": [1, 2]}`},
		{Level: "warn", Text: `quoted "e, f]" and \ and \"g,`},
		{Level: "error", Text: "<h> & é, \u2028\n,]"},
		{},
	}

	for _, lines := range [][]ports.CodeLog{nil, {}, sent[:1], sent} {
		var b bytes.Buffer
		require.NoError(t, writeFrame(&b, message{Kind: kindResult, ID: 7, Result: &result{Logs: logs{lines: lines}}}, maxFrame))

		m, err := readFrame(&b, maxFrame)
		require.NoError(t, err)

		if len(lines) == 0 {
			lines = nil
		}

		assert.Equal(t, logs{lines: lines}, m.Result.Logs)
	}

	_, err := readFrame(bytes.NewReader(binary.BigEndian.AppendUint32(nil, 37)), maxFrame)
	require.Error(t, err)

	for _, raw := range []string{`{"logs":{}}`, `{"logs":"x"}`, `{"logs":[1]}`, `{"logs":[{"Text":2}]}`} {
		var r result
		assert.Error(t, json.Unmarshal([]byte(raw), &r), raw)
	}
}
