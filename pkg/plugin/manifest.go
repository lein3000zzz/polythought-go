package plugin

import "encoding/json"

// Manifest says what a plugin is and what it brings: its nodes, triggers, credential types
// and scenarios (D284), and the permissions and settings it asks for. Its JSON is
// manifest.json's, and what Describe answers; its node types are shaped as the API's
// GET /node-types describes the built-in ones.
type Manifest struct {
	// ID names the plugin, a-z, 0-9 and dashes, starting with a letter; its nodes are
	// plugin.<id>.<name> (D285).
	ID          string `json:"id"`
	Version     string `json:"version"` // semver, like 1.2.0
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// SDK is the version of this SDK the plugin was built with, set by Serve.
	SDK string `json:"sdk,omitempty"`

	// Permissions are what it may ask the host for, which an admin approves (5c): see
	// PermFetch.
	Permissions []string `json:"permissions,omitempty"`
	// Settings are its own, shown on the Config page (5c).
	Settings []Setting `json:"settings,omitempty"`
	// Metrics are those it records through Host.Metric, named plugin_<id>_<name>.
	Metrics []Metric `json:"metrics,omitempty"`

	Nodes           []NodeType       `json:"nodes,omitempty"`
	Triggers        []NodeType       `json:"triggers,omitempty"`
	CredentialTypes []CredentialType `json:"credentialTypes,omitempty"`
	Scenarios       []Scenario       `json:"scenarios,omitempty"`
}

// Permissions a manifest may ask for.
const (
	// PermFetch, with a host, lets Fetch reach that host: fetch:api.example.com, on any
	// port, its subdomains not. PermFetchAny lets it reach any public address.
	PermFetch    = "fetch:"
	PermFetchAny = "fetch:*"

	// PermWallets lets Wallets give the workflow's owner's wallets: their names and
	// addresses, which tie them to their money on chain.
	PermWallets = "wallets"
)

// NodeType describes a node or a trigger, as a built-in one is: its name within the
// plugin, ports, settings and items.
type NodeType struct {
	Name        string   `json:"name"` // within the plugin, like forecast: a letter, then letters and digits
	Label       string   `json:"label,omitempty"`
	Description string   `json:"description,omitempty"`
	Inputs      []string `json:"inputs,omitempty"` // a trigger has none
	Outputs     []string `json:"outputs"`
	Params      []Param  `json:"params,omitempty"`

	// OutputsParam names a list param whose values are more output ports, after Outputs;
	// with OutputsColumn, a table, that text column of each row naming a port.
	OutputsParam  string `json:"outputsParam,omitempty"`
	OutputsColumn string `json:"outputsColumn,omitempty"`

	// Items describes what it emits; nil when nobody knows.
	Items *Items `json:"items,omitempty"`

	// Remembers says it keeps data in its store across runs (Host.Get and Set): only
	// then may it, and its settings show what it keeps.
	Remembers bool `json:"remembers,omitempty"`
}

// Param is one setting of a node: how the editor shows it, and how the host reads it
// before the node gets it, expressions worked out (D286).
type Param struct {
	Name        string `json:"name"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	// Kind is one of string, number (whole), decimal, bool, list (of text), duration,
	// wallet, tokens, address, table or credential.
	Kind     string `json:"kind"`
	Required bool   `json:"required,omitempty"`
	Default  any    `json:"default,omitempty"`
	// Options are the allowed values of text, or of each element of a list.
	Options []string `json:"options,omitempty"`
	// Sensitive values, like a URL with a token in it, are personal: sharing a workflow
	// leaves them out.
	Sensitive bool `json:"sensitive,omitempty"`
	// CredentialTypes, on a credential param, are the kinds it takes: the plugin's own by
	// their names, built-in ones by their full names, like http.header.
	CredentialTypes []string `json:"credentialTypes,omitempty"`
	// Field marks text, or each element of a list, as a path to a field of the items the
	// node receives; FieldKinds, when present, are the kinds it may be; Input the port
	// whose items, main when empty.
	Field      bool     `json:"field,omitempty"`
	FieldKinds []string `json:"fieldKinds,omitempty"`
	Input      string   `json:"input,omitempty"`
	Multiline  bool     `json:"multiline,omitempty"`
	// Columns are a table's cells, keyed by name.
	Columns []Param `json:"columns,omitempty"`
}

// Items describes what a node emits: fields of its own, or the items it received, whole
// with fields it adds, with only some fields (Picked), or an item for each entry of a list
// (Split).
type Items struct {
	Fields []Field `json:"fields,omitempty"`
	Passed bool    `json:"passed,omitempty"`
	Picked string  `json:"picked,omitempty"`
	Split  string  `json:"split,omitempty"`
}

// Field is one field of an item: text, number, decimal, bool, time, list (its elements
// Of), object (its Fields) or any.
type Field struct {
	Name        string   `json:"name"` // empty for a list's elements
	Kind        string   `json:"kind"`
	Description string   `json:"description,omitempty"`
	Optional    bool     `json:"optional,omitempty"`
	Nullable    bool     `json:"nullable,omitempty"`
	Options     []string `json:"options,omitempty"`
	Of          *Field   `json:"of,omitempty"`
	Fields      []Field  `json:"fields,omitempty"`
}

// CredentialType is a kind of credential users keep in their vault for the plugin's
// nodes: its fields, secret ones stored encrypted.
type CredentialType struct {
	Name        string            `json:"name"` // within the plugin; plugin.<id>.<name> to the rest
	Label       string            `json:"label"`
	Description string            `json:"description,omitempty"`
	Fields      []CredentialField `json:"fields"`
	// Testable says the plugin tests the values against its service, on the Credentials
	// page's Test.
	Testable bool `json:"testable,omitempty"`
}

type CredentialField struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Secret      bool   `json:"secret,omitempty"`
}

// Setting is one of the plugin's own settings, an admin's on the Config page (5c).
type Setting struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Kind        string   `json:"kind"` // string, int, bool, duration, enum or list
	Default     any      `json:"default,omitempty"`
	Options     []string `json:"options,omitempty"`
	Secret      bool     `json:"secret,omitempty"`
}

// Metric is one of the plugin's metrics: a counter, or a histogram of the values it
// observes, in Buckets when they are given. Labels are the names of the labels it is
// recorded with, every time all of them.
type Metric struct {
	Name    string    `json:"name"` // a-z, 0-9 and underscores, starting with a letter
	Kind    string    `json:"kind"` // counter or histogram
	Help    string    `json:"help"`
	Labels  []string  `json:"labels,omitempty"`
	Buckets []float64 `json:"buckets,omitempty"`
}

// Scenario is a workflow to start from, in the library, as the built-in scenarios are
// written: nodes with their settings and positions, and connections. Its nodes may be the
// plugin's, as plugin.<id>.<name>, and built-in ones.
type Scenario struct {
	ID          string          `json:"id"` // within the plugin
	Name        string          `json:"name"`
	Summary     string          `json:"summary"`
	Description string          `json:"description,omitempty"`
	Nodes       json.RawMessage `json:"nodes"`
	Connections json.RawMessage `json:"connections"`
}
