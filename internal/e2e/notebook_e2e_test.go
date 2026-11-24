//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// notebook is what a notebook does with Polythought, through the Python SDK: it publishes
// forecasts, serves a model while a workflow reads the one and asks the other, and reads
// back what the workflow recorded.
const notebook = `
import json, os, sys, threading, time

import pandas as pd

from polythought import Client, serve

pt = Client(os.environ["PT_URL"], token=os.environ["PT_TOKEN"])
forecasts, bets, model = os.environ["PT_FORECASTS"], os.environ["PT_BETS"], os.environ["PT_MODEL"]

# Published: a table of forecasts, replaced at once.
pt.datasets.create(forecasts, key_field="city")
pt.datasets.replace_frame(forecasts, pd.DataFrame({"city": ["NYC", "LDN"], "high": [51.5, 9.0]}))
pt.datasets.create(bets, keep_days=90)

# Served: the model the workflow asks, from this process.
def predict(items):
    return [{"buy": item["row"]["high"] > 50} for item in items]

stop = threading.Event()
threading.Thread(
    target=serve, args=(predict,), daemon=True,
    kwargs={"key": os.environ["PT_KEY"], "url": os.environ["PT_URL"], "worker": "notebook", "version": "0.1.0", "stop": stop},
).start()

deadline = time.time() + 30
while not pt.models.get(model)["workers"]:
    if time.time() > deadline:
        sys.exit("the worker never connected")
    time.sleep(0.2)

# The workflow, run by hand: it waits for the model's answers.
run = pt.request("POST", f"/workflows/{os.environ['PT_WORKFLOW']}/run", timeout=120).json()
stop.set()

# Read back: what it recorded.
print(json.dumps({"status": run["status"], "error": run.get("error"), "bets": pt.datasets.read(bets)}))
`

// pythonVenv is a fresh virtualenv with the SDK the stack serves, and pandas: the SDK
// installed as a notebook would, from the instance.
func pythonVenv(t *testing.T, base string) string {
	t.Helper()

	python := env("E2E_PYTHON", "python3")

	out, err := exec.Command(python, "-c", "import sys; print(sys.version_info >= (3, 10))").Output()
	require.NoError(t, err, "no Python at %s: set E2E_PYTHON (make e2e PYTHON=…) to a Python 3.10 or newer", python)
	require.Equal(t, "True", strings.TrimSpace(string(out)), "%s is older than 3.10: set E2E_PYTHON to a newer one", python)

	dir := t.TempDir()
	venv := filepath.Join(dir, "venv")

	run(t, dir, python, "-m", "venv", venv)

	u, err := url.Parse(base)
	require.NoError(t, err)

	run(t, dir, filepath.Join(venv, "bin", "pip"), "install", "--quiet", "--disable-pip-version-check",
		"--find-links", base+"/api/v1/sdk/python/", "--trusted-host", u.Hostname(), "polythought[pandas]")

	return filepath.Join(venv, "bin", "python")
}

// run runs a command in dir, the stack's host bypassing any proxy, failing with its output.
func run(t *testing.T, dir, name string, args ...string) string {
	t.Helper()

	var out bytes.Buffer

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NO_PROXY=localhost,127.0.0.1", "no_proxy=localhost,127.0.0.1")
	cmd.Stdout, cmd.Stderr = &out, &out

	require.NoError(t, cmd.Run(), "%s %s:\n%s", name, strings.Join(args, " "), out.String())

	return out.String()
}

// The whole loop, as a notebook and a workflow close it: the notebook publishes forecasts and
// serves a model; the workflow reads every forecast, asks the model about each and records
// what it said; the notebook reads the bets back.
func TestNotebookLoop(t *testing.T) {
	base := env("E2E_BASE_URL", "http://localhost:3100")
	b := signIn(t, base)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)

	forecasts, bets, model := "loop-forecasts-"+suffix, "loop-bets-"+suffix, "loop-model-"+suffix

	// At the browser: a token for the notebook, the model and its worker key.
	var token struct {
		Token string `json:"token"`
	}

	b.call(http.MethodPost, "/api/v1/tokens", map[string]any{"name": "notebook " + suffix, "scopes": []string{"read", "build", "data"}},
		http.StatusCreated, &token)
	b.call(http.MethodPost, "/api/v1/models", map[string]string{"name": model, "kind": "worker"}, http.StatusCreated, nil)

	var key struct {
		Secret string `json:"secret"`
	}

	b.call(http.MethodPost, "/api/v1/models/"+model+"/keys", map[string]string{"name": "notebook"}, http.StatusCreated, &key)

	// The workflow: every forecast, the model asked about each, its answers recorded.
	var wf struct {
		ID string `json:"id"`
	}

	b.call(http.MethodPost, "/api/v1/workflows", map[string]any{
		"name": "Loop " + suffix,
		"nodes": []map[string]any{
			{"id": "every", "type": "trigger.schedule", "params": map[string]any{"interval": "1h", "runOnStart": false}, "position": map[string]any{"x": 0, "y": 0}},
			{"id": "read", "type": "data.read", "params": map[string]any{"dataset": forecasts, "read": "every row"}, "position": map[string]any{"x": 300, "y": 0}},
			{"id": "ask", "type": "data.ask", "params": map[string]any{"model": model, "fields": []string{"row"}, "wait": "30s"}, "position": map[string]any{"x": 600, "y": 0}},
			{"id": "record", "type": "data.record", "params": map[string]any{"dataset": bets}, "position": map[string]any{"x": 900, "y": 0}},
		},
		"connections": []map[string]string{
			{"from": "every", "fromPort": "main", "to": "read", "toPort": "main"},
			{"from": "read", "fromPort": "main", "to": "ask", "toPort": "main"},
			{"from": "ask", "fromPort": "main", "to": "record", "toPort": "main"},
		},
	}, http.StatusCreated, &wf)

	// The notebook, in its own process, with the SDK the stack serves.
	python := pythonVenv(t, base)
	dir := t.TempDir()
	script := filepath.Join(dir, "notebook.py")
	require.NoError(t, os.WriteFile(script, []byte(notebook), 0o600))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	var out, errs bytes.Buffer

	cmd := exec.CommandContext(ctx, python, "-I", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"PT_URL="+base, "PT_TOKEN="+token.Token, "PT_KEY="+key.Secret, "PT_MODEL="+model,
		"PT_FORECASTS="+forecasts, "PT_BETS="+bets, "PT_WORKFLOW="+wf.ID,
		"NO_PROXY=localhost,127.0.0.1", "no_proxy=localhost,127.0.0.1")
	cmd.Stdout, cmd.Stderr = &out, &errs

	require.NoError(t, cmd.Run(), "the notebook:\n%s", errs.String())

	var got struct {
		Status string           `json:"status"`
		Error  string           `json:"error"`
		Bets   []map[string]any `json:"bets"`
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &got), out.String())
	require.Equal(t, "succeeded", got.Status, got.Error)

	// Each forecast's bet, with the model's answer, version and worker, and where it came from.
	require.Len(t, got.Bets, 2)

	byCity := map[string]map[string]any{}
	for _, bet := range got.Bets {
		byCity[bet["row"].(map[string]any)["city"].(string)] = bet
	}

	for city, buy := range map[string]bool{"NYC": true, "LDN": false} {
		bet := byCity[city]
		require.NotNil(t, bet, city)

		prediction := bet["prediction"].(map[string]any)
		assert.Equal(t, map[string]any{"buy": buy}, prediction["answer"], city)
		assert.Equal(t, model, prediction["model"])
		assert.Equal(t, "0.1.0", prediction["version"])
		assert.Equal(t, "notebook", prediction["worker"])
		assert.Equal(t, wf.ID, bet["_workflow"])
		assert.Equal(t, "paper", bet["_mode"])
	}
}
