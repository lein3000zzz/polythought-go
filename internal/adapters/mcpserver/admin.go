package mcpserver

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// The admin's tools for users, invites and requests to join (D410), for an admin's agent
// with the admin scope: no user's email is given, a user found by an email the agent names;
// people are invited rather than given passwords, unless the instance lets agents set them.
// What touches a user's role, account, caps or code waits for the admin where they say so.

// Admin is an admin's work on users, invites and requests: the auth service.
type Admin interface {
	ListUsers(ctx context.Context, actor auth.Principal, f ports.PageFilter) (ports.Page[domain.User], error)
	FindUser(ctx context.Context, actor auth.Principal, email string) (domain.User, error)
	UpdateUser(ctx context.Context, actor auth.Principal, id domain.UserID, upd auth.UserUpdate) (domain.User, error)
	CreateUser(ctx context.Context, actor auth.Principal, email, password string, role domain.Role) (domain.User, error)
	SendVerificationLink(ctx context.Context, actor auth.Principal, id domain.UserID) error
	InviteEmail() bool
	InviteByEmail(ctx context.Context, actor auth.Principal, email string) (domain.Invite, bool, error)
	CreateInvite(ctx context.Context, actor auth.Principal, spec auth.InviteSpec) (domain.Invite, error)
	RevokeInvite(ctx context.Context, actor auth.Principal, id domain.InviteID) error
	ListInvites(ctx context.Context, actor auth.Principal) ([]domain.Invite, error)
	ListJoinRequests(ctx context.Context, actor auth.Principal) ([]domain.JoinRequest, error)
	ApproveJoin(ctx context.Context, actor auth.Principal, id domain.JoinRequestID) (domain.Invite, bool, error)
	DenyJoin(ctx context.Context, actor auth.Principal, id domain.JoinRequestID) error
}

type capsOut struct {
	PerOrder *int `json:"perOrder,omitempty" jsonschema:"the most one live buy may spend, whole dollars; 0 for none; absent, the instance's"`
	PerDay   *int `json:"perDay,omitempty" jsonschema:"the most a day of live buys may spend; 0 for none; absent, the instance's"`
}

type agentCapsOut struct {
	Dollars      string `json:"dollars,omitempty" jsonschema:"the most their agent nodes may spend a month, in dollars; 0 for none; absent, the instance's"`
	InputTokens  *int64 `json:"inputTokens,omitempty"`
	OutputTokens *int64 `json:"outputTokens,omitempty"`
	TotalTokens  *int64 `json:"totalTokens,omitempty"`
}

type userOut struct {
	ID         string       `json:"id"`
	Role       string       `json:"role"`
	Disabled   bool         `json:"disabled,omitempty"`
	Verified   bool         `json:"verified" jsonschema:"their email address is verified"`
	CodeChosen bool         `json:"codeChosen,omitempty" jsonschema:"picked to write code, where code is for chosen users"`
	LiveCaps   capsOut      `json:"liveCaps" jsonschema:"their own caps on live buys"`
	AgentCaps  agentCapsOut `json:"agentCaps" jsonschema:"their own caps on their agent nodes' spend a month"`
	CreatedAt  time.Time    `json:"createdAt"`
}

type userList struct {
	Users []userOut `json:"users"`
	Next  string    `json:"next,omitempty"`
}

type findUserIn struct {
	Email string `json:"email" jsonschema:"the user's email address, as the admin gave it to you"`
}

type userIDIn struct {
	ID string `json:"id" jsonschema:"the user's ID"`
}

type inviteIDIn struct {
	ID string `json:"id" jsonschema:"the invite's ID, as list_invites gives it"`
}

type requestIDIn struct {
	ID string `json:"id" jsonschema:"the request's ID, as list_requests gives it"`
}

type updateUserIn struct {
	ID            string        `json:"id" jsonschema:"the user's ID"`
	Role          string        `json:"role,omitempty" jsonschema:"admin or user"`
	Disabled      *bool         `json:"disabled,omitempty" jsonschema:"true disables the account: signed out, their workflows stopped, their kill switch on"`
	CodeChosen    *bool         `json:"codeChosen,omitempty" jsonschema:"let them write code, where code is for chosen users"`
	EmailVerified bool          `json:"emailVerified,omitempty" jsonschema:"mark their address verified, the admin's word for it"`
	LiveCaps      *capsOut      `json:"liveCaps,omitempty" jsonschema:"replace their own caps on live buys; a cap left out is the instance's again"`
	AgentCaps     *agentCapsOut `json:"agentCaps,omitempty" jsonschema:"replace their own caps on their agent nodes' spend"`
}

type createUserIn struct {
	Email    string `json:"email" jsonschema:"their email address"`
	Password string `json:"password" jsonschema:"their password, as the admin gave it; they should change it"`
}

type inviteIn struct {
	Email     string `json:"email,omitempty" jsonschema:"mail the invite to this address, which alone signs up with it, where mail is set up; else a link"`
	MaxUses   int    `json:"maxUses,omitempty" jsonschema:"a link's: how many may sign up with it; 1 unless said"`
	ExpiresIn string `json:"expiresIn,omitempty" jsonschema:"a link's: how long it works, like 72h; the instance's invite lifetime unless said"`
}

type inviteOut struct {
	ID        string    `json:"id"`
	Mailed    bool      `json:"mailed,omitempty" jsonschema:"mailed to an address, which alone signs up with it"`
	Link      string    `json:"link,omitempty" jsonschema:"the link to sign up with, to pass on"`
	ExpiresAt time.Time `json:"expiresAt"`
	MaxUses   int       `json:"maxUses"`
	Used      int       `json:"used"`
	Revoked   bool      `json:"revoked,omitempty"`
}

type inviteList struct {
	Invites []inviteOut `json:"invites"`
}

// doneOut is a tool's answer that it did what it was asked.
type doneOut struct {
	Done bool `json:"done"`
}

type requestsIn struct {
	Email string `json:"email,omitempty" jsonschema:"only the request of this address, as the admin gave it to you"`
}

type requestOut struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	Confirmed bool      `json:"confirmed" jsonschema:"they opened the link mailed to them, so the address is theirs"`
	Decided   string    `json:"decided,omitempty" jsonschema:"approved or denied; absent while it waits"`
	Untrusted Untrusted `json:"untrusted,omitempty" jsonschema:"their note, their words"`
}

type requestList struct {
	Requests []requestOut `json:"requests"`
	Notice   string       `json:"notice,omitempty"`
}

type approvedOut struct {
	Mailed bool   `json:"mailed" jsonschema:"the invite was mailed to them"`
	Link   string `json:"link,omitempty" jsonschema:"the invite's link, to pass on, where it was not mailed"`
}

func (s *Server) addAdminTools(srv *mcp.Server) {
	add(s, srv, Tool[page, userList]{
		Name: "list_users", Title: "List users", Scope: domain.ScopeAdmin, ReadOnly: true,
		Description: "The instance's users, by ID, with their role, state, caps and code access; never their emails. " +
			"find_user finds one by an email the admin gives you.",
		Handle: s.listUsers,
	})

	add(s, srv, Tool[findUserIn, userOut]{
		Name: "find_user", Title: "Find a user", Scope: domain.ScopeAdmin, ReadOnly: true,
		Description: "The user with an email address the admin gave you.",
		Handle:      s.findUser,
	})

	add(s, srv, Tool[updateUserIn, userOut]{
		Name: "update_user", Title: "Change a user", Scope: domain.ScopeAdmin,
		Description: "Change a user's role, disable or enable their account, mark their address verified, let them write code, " +
			"or set their own caps. A role change, disabling, raising caps and letting them write code may wait for the admin " +
			"to confirm, as get_confirmation then says. The last admin is never demoted or disabled.",
		Handle: s.updateUser,
	})

	add(s, srv, Tool[userIDIn, doneOut]{
		Name: "send_verification_link", Title: "Mail a verification link", Scope: domain.ScopeAdmin,
		Description: "Mail a user a new link to verify their address, where mail is set up.",
		Handle:      s.sendVerification,
	})

	add(s, srv, Tool[createUserIn, userOut]{
		Name: "create_user", Title: "Create an account", Scope: domain.ScopeAdmin,
		Description: "Create an account with a password the admin gave you, verified, as a user. Only where the instance lets " +
			"agents set passwords; else invite them, which sends no password through you.",
		Handle: s.createUser,
	})

	add(s, srv, Tool[inviteIn, inviteOut]{
		Name: "invite", Title: "Invite someone", Scope: domain.ScopeAdmin,
		Description: "Invite someone to sign up: mailed to an address, where mail is set up, which alone signs up with it; or a " +
			"link to pass on, for as many as maxUses.",
		Handle: s.invite,
	})

	add(s, srv, Tool[struct{}, inviteList]{
		Name: "list_invites", Title: "List invites", Scope: domain.ScopeAdmin, ReadOnly: true,
		Description: "The invites, newest first, with how many signed up with each, and the links of those not mailed; never the addresses mailed to.",
		Handle:      s.listInvites,
	})

	add(s, srv, Tool[inviteIDIn, deletedOut]{
		Name: "revoke_invite", Title: "Revoke an invite", Scope: domain.ScopeAdmin,
		Description: "Revoke an invite by its ID: no one signs up with it any more.",
		Handle:      s.revokeInvite,
	})

	add(s, srv, Tool[requestsIn, requestList]{
		Name: "list_requests", Title: "List requests to join", Scope: domain.ScopeAdmin, ReadOnly: true,
		Description: "Requests to join, newest first, by ID, with their notes; never their addresses. Give one the admin named " +
			"to find its request.",
		Handle: s.listRequests,
	})

	add(s, srv, Tool[requestIDIn, approvedOut]{
		Name: "approve_request", Title: "Approve a request to join", Scope: domain.ScopeAdmin,
		Description: "Approve a request to join by its ID: an invite is mailed to them.",
		Handle:      s.approveRequest,
	})

	add(s, srv, Tool[requestIDIn, deletedOut]{
		Name: "deny_request", Title: "Deny a request to join", Scope: domain.ScopeAdmin,
		Description: "Deny a request to join by its ID; they are told where the instance says so.",
		Handle:      s.denyRequest,
	})
}

func userOf(u domain.User) userOut {
	out := userOut{
		ID: string(u.ID), Role: string(u.Role), Disabled: u.Disabled(), Verified: !u.EmailVerifiedAt.IsZero(), CodeChosen: u.CodeChosen,
		LiveCaps: capsOut{PerOrder: u.LiveCaps.PerOrder, PerDay: u.LiveCaps.PerDay},
		AgentCaps: agentCapsOut{
			InputTokens: u.AgentCaps.InputTokens, OutputTokens: u.AgentCaps.OutputTokens, TotalTokens: u.AgentCaps.TotalTokens,
		},
		CreatedAt: u.CreatedAt,
	}

	if u.AgentCaps.Dollars != nil {
		out.AgentCaps.Dollars = u.AgentCaps.Dollars.String()
	}

	return out
}

func (s *Server) listUsers(ctx context.Context, p auth.Principal, in page) (userList, error) {
	got, err := s.admin.ListUsers(ctx, p, in.filter())
	if err != nil {
		return userList{}, err
	}

	out := userList{Users: make([]userOut, len(got.Items)), Next: got.NextCursor}
	for i, u := range got.Items {
		out.Users[i] = userOf(u)
	}

	return out, nil
}

func (s *Server) findUser(ctx context.Context, p auth.Principal, in findUserIn) (userOut, error) {
	u, err := s.admin.FindUser(ctx, p, in.Email)
	if err != nil {
		return userOut{}, err
	}

	return userOf(u), nil
}

func (s *Server) updateUser(ctx context.Context, p auth.Principal, in updateUserIn) (userOut, error) {
	upd := auth.UserUpdate{Disabled: in.Disabled, CodeChosen: in.CodeChosen}

	if in.Role != "" {
		role := domain.Role(in.Role)
		upd.Role = &role
	}

	if in.EmailVerified {
		upd.EmailVerified = &in.EmailVerified
	}

	if in.LiveCaps != nil {
		upd.LiveCaps = &domain.LiveCaps{PerOrder: in.LiveCaps.PerOrder, PerDay: in.LiveCaps.PerDay}
	}

	if in.AgentCaps != nil {
		caps := domain.AgentCaps{InputTokens: in.AgentCaps.InputTokens, OutputTokens: in.AgentCaps.OutputTokens, TotalTokens: in.AgentCaps.TotalTokens}

		if in.AgentCaps.Dollars != "" {
			d, err := decimal.NewFromString(in.AgentCaps.Dollars)
			if err != nil {
				return userOut{}, invalid("agentCaps.dollars is a number of dollars, like 20.00")
			}

			caps.Dollars = &d
		}

		upd.AgentCaps = &caps
	}

	u, err := s.admin.UpdateUser(ctx, p, domain.UserID(in.ID), upd)
	if err != nil {
		return userOut{}, err
	}

	return userOf(u), nil
}

func (s *Server) sendVerification(ctx context.Context, p auth.Principal, in userIDIn) (doneOut, error) {
	if err := s.admin.SendVerificationLink(ctx, p, domain.UserID(in.ID)); err != nil {
		return doneOut{}, err
	}

	return doneOut{Done: true}, nil
}

func (s *Server) createUser(ctx context.Context, p auth.Principal, in createUserIn) (userOut, error) {
	u, err := s.admin.CreateUser(ctx, p, in.Email, in.Password, domain.RoleUser)
	if err != nil {
		return userOut{}, err
	}

	return userOf(u), nil
}

// inviteLink is where an invite's code signs up, as the Invites page copies it.
func (s *Server) inviteLink(code string) string {
	if code == "" {
		return ""
	}

	return s.publicURL + "/register?invite=" + url.QueryEscape(code)
}

func (s *Server) inviteOf(inv domain.Invite) inviteOut {
	out := inviteOut{
		ID: string(inv.ID), Mailed: inv.Email != "", ExpiresAt: inv.ExpiresAt, MaxUses: inv.MaxUses, Used: inv.Used,
		Revoked: !inv.RevokedAt.IsZero(),
	}

	if !out.Mailed && !out.Revoked {
		out.Link = s.inviteLink(inv.Code)
	}

	return out
}

func (s *Server) invite(ctx context.Context, p auth.Principal, in inviteIn) (inviteOut, error) {
	if strings.TrimSpace(in.Email) != "" {
		if !s.admin.InviteEmail() {
			return inviteOut{}, fmt.Errorf("%w: invites are not mailed here: inviting by email is off, or mail is not set up; "+
				"make a link instead, leaving email out", domain.ErrInviteEmailOff)
		}

		inv, mailed, err := s.admin.InviteByEmail(ctx, p, in.Email)
		if err != nil {
			return inviteOut{}, err
		}

		out := s.inviteOf(inv)
		if !mailed {
			out.Mailed, out.Link = false, s.inviteLink(inv.Code) // the mail did not go: the link, to pass on
		}

		return out, nil
	}

	spec := auth.InviteSpec{MaxUses: max(in.MaxUses, 1)}

	if in.ExpiresIn != "" {
		d, err := time.ParseDuration(in.ExpiresIn)
		if err != nil || d <= 0 {
			return inviteOut{}, invalid("expiresIn is a duration, like 72h")
		}

		spec.ExpiresAt = time.Now().Add(d)
	}

	inv, err := s.admin.CreateInvite(ctx, p, spec)
	if err != nil {
		return inviteOut{}, err
	}

	return s.inviteOf(inv), nil
}

func (s *Server) listInvites(ctx context.Context, p auth.Principal, _ struct{}) (inviteList, error) {
	list, err := s.admin.ListInvites(ctx, p)
	if err != nil {
		return inviteList{}, err
	}

	out := inviteList{Invites: make([]inviteOut, len(list))}
	for i, inv := range list {
		out.Invites[i] = s.inviteOf(inv)
	}

	return out, nil
}

func (s *Server) revokeInvite(ctx context.Context, p auth.Principal, in inviteIDIn) (deletedOut, error) {
	if err := s.admin.RevokeInvite(ctx, p, domain.InviteID(in.ID)); err != nil {
		return deletedOut{}, err
	}

	return deletedOut{Deleted: in.ID}, nil
}

func (s *Server) listRequests(ctx context.Context, p auth.Principal, in requestsIn) (requestList, error) {
	list, err := s.admin.ListJoinRequests(ctx, p)
	if err != nil {
		return requestList{}, err
	}

	want := strings.ToLower(strings.TrimSpace(in.Email))
	out := requestList{Requests: []requestOut{}}

	for _, r := range list {
		if want != "" && strings.ToLower(r.Email) != want {
			continue
		}

		o := requestOut{ID: string(r.ID), CreatedAt: r.CreatedAt, Confirmed: !r.ConfirmedAt.IsZero()}

		switch {
		case r.DecidedAt.IsZero():
		case r.Approved:
			o.Decided = "approved"
		default:
			o.Decided = "denied"
		}

		if r.Note != "" {
			o.Untrusted, out.Notice = Untrusted{"note": r.Note}, Notice
		}

		out.Requests = append(out.Requests, o)
	}

	return out, nil
}

func (s *Server) approveRequest(ctx context.Context, p auth.Principal, in requestIDIn) (approvedOut, error) {
	inv, mailed, err := s.admin.ApproveJoin(ctx, p, domain.JoinRequestID(in.ID))
	if err != nil {
		return approvedOut{}, err
	}

	out := approvedOut{Mailed: mailed}
	if !mailed {
		out.Link = s.inviteLink(inv.Code)
	}

	return out, nil
}

func (s *Server) denyRequest(ctx context.Context, p auth.Principal, in requestIDIn) (deletedOut, error) {
	if err := s.admin.DenyJoin(ctx, p, domain.JoinRequestID(in.ID)); err != nil {
		return deletedOut{}, err
	}

	return deletedOut{Deleted: in.ID}, nil
}
