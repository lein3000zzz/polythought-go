package domain

import "time"

type TemplateID string

// Template is a workflow to start from: a built-in scenario, or a copy a member shared.
// Starting from one makes a new workflow; the template itself never runs.
type Template struct {
	ID       TemplateID
	AuthorID UserID     // empty for built-in scenarios
	SourceID WorkflowID // the workflow a shared copy came from, if it still exists

	Name        string
	Summary     string // one line, for the library's list
	Description string // what it does and what to change, in plain paragraphs

	Nodes       []NodeSpec
	Connections []Connection

	CreatedAt time.Time
	UpdatedAt time.Time
}

func (t Template) BuiltIn() bool {
	return t.AuthorID == ""
}
