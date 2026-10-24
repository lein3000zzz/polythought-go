package domain

// ActorHow is how an agent or an API token came to be behind a workflow's runs (D407).
type ActorHow string

const (
	ActorRan        ActorHow = "run"         // it ran this one by hand
	ActorSwitchedOn ActorHow = "switched_on" // it switched the workflow on
	ActorChanged    ActorHow = "changed"     // it changed the workflow while it was on
)

// Actor is an agent or an API token that acted for the user (D407), named as it was then.
// Its zero value is the user themself.
type Actor struct {
	GrantID GrantID
	TokenID APITokenID
	Name    string // the agent's name, or the token's
	How     ActorHow
	// Confirmed says the user confirmed it, as it waited for them (D406).
	Confirmed bool
}

// IsUser says the user acted themself.
func (a Actor) IsUser() bool { return a.GrantID == "" && a.TokenID == "" }
