package notify

import (
	"context"
	"errors"
	"html"
	"strings"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.PluginNotifier = (*Service)(nil)

var WorkflowPaused = Kind{
	Key: "workflow_paused", Group: groupWorkflows,
	Label: "A workflow is switched off for you",
	Description: "One of your active workflows was switched off without you: a plugin it uses was disabled or removed, " +
		"or is no longer yours to use; its triggers started it more often than the instance allows; or an admin " +
		"switched it off, or holds it off until they release it. Also when an admin releases it.",
	Default: true, EmailDefault: true,
}

var PluginCrashed = Kind{
	Key: "plugin_crashed", Group: groupAdmin,
	Label: "A plugin keeps crashing",
	Description: "A plugin crashed 5 times in 10 minutes, or would not start, and is stopped until an admin starts it " +
		"again on the Plugins page.",
	Default: true, EmailDefault: true, Admins: true,
}

var JoinRequested = Kind{
	Key: "join_requested", Group: groupAdmin,
	Label: "Someone asks to join",
	Description: "Someone confirmed a request to join, with their address and why they would like in. " +
		"Approve or deny it on the Invites page.",
	Default: true, EmailDefault: true, Admins: true,
}

// AdminLister lists the instance's admins.
type AdminLister interface {
	Admins(ctx context.Context) ([]domain.User, error)
}

// WithAdmins tells admins about plugins that crashed for good (D320), requests to join
// (D394) and newer releases (D424).
func WithAdmins(a AdminLister) Option {
	return func(s *Service) { s.admins = a }
}

type pausedWorkflow struct {
	owner    domain.UserID
	workflow domain.Workflow
	why      string
	how      pausedHow
}

// pausedHow is what was done to a workflow: paused, held off by an admin, or released.
type pausedHow int

const (
	howPaused pausedHow = iota
	howHeld
	howReleased
)

type crashedPlugin struct {
	plugin string
	why    string
}

type joinRequest struct {
	email string
	note  string
}

// WorkflowPaused queues telling a workflow's owner why it was paused (D319). It never
// blocks: when the queue is full, it goes untold.
func (s *Service) WorkflowPaused(owner domain.UserID, w domain.Workflow, why string) {
	s.queuePaused(pausedWorkflow{owner: owner, workflow: w, why: why, how: howPaused})
}

// WorkflowHeld queues telling a workflow's owner an admin holds it off, and why (D435).
func (s *Service) WorkflowHeld(owner domain.UserID, w domain.Workflow, why string) {
	s.queuePaused(pausedWorkflow{owner: owner, workflow: w, why: why, how: howHeld})
}

// WorkflowReleased queues telling a workflow's owner an admin released it (D435).
func (s *Service) WorkflowReleased(owner domain.UserID, w domain.Workflow) {
	s.queuePaused(pausedWorkflow{owner: owner, workflow: w, how: howReleased})
}

func (s *Service) queuePaused(p pausedWorkflow) {
	select {
	case s.paused <- p:
	default:
		s.log.Warn(context.Background(), "Paused workflow notification dropped: too many at once", "workflow_id", p.workflow.ID)
	}
}

// PluginCrashed queues telling the admins that a plugin stopped for good (D320).
func (s *Service) PluginCrashed(plugin, why string) {
	select {
	case s.crashed <- crashedPlugin{plugin: plugin, why: why}:
	default:
		s.log.Warn(context.Background(), "Crashed plugin notification dropped: too many at once", "plugin", plugin)
	}
}

// JoinRequested queues telling the admins someone asks to join (D394). It never blocks:
// when the queue is full, it goes untold, the request listed all the same.
func (s *Service) JoinRequested(email, note string) {
	select {
	case s.requested <- joinRequest{email: email, note: note}:
	default:
		s.log.Warn(context.Background(), "Join request notification dropped: too many at once")
	}
}

func (s *Service) workflowPaused(ctx context.Context, p pausedWorkflow) {
	name := "Your workflow “" + p.workflow.Name + "”"
	subject, said, icon := name+" was paused", name+" was paused: "+p.why+". Turn it on again once it can run.", "⏸ "

	switch p.how {
	case howHeld:
		subject = name + " is held off by an admin"
		said = name + " was switched off, and an admin holds it off, saying “" + p.why + "”. It can be switched on again " +
			"once an admin releases it."
	case howReleased:
		subject, said, icon = name+" was released", "An admin released "+lowerFirst(name)+": you may switch it on again.", "▶️ "
	}

	s.tell(ctx, p.owner, WorkflowPaused, subject, said, "Open the workflow", s.publicURL+"/workflows/"+string(p.workflow.ID), icon)
}

// lowerFirst is s with its first letter small: "Your workflow" read mid-sentence.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}

	return strings.ToLower(s[:1]) + s[1:]
}

func (s *Service) pluginCrashed(ctx context.Context, c crashedPlugin) {
	if s.admins == nil {
		return
	}

	admins, err := s.admins.Admins(ctx)
	if err != nil {
		s.log.Warn(ctx, "Crashed plugin notification skipped", "plugin", c.plugin, logging.Err(err))
		return
	}

	said := "The plugin " + c.plugin + " is stopped: " + c.why + ". Start it again on the Plugins page once it is fixed."

	for _, a := range admins {
		s.tell(ctx, a.ID, PluginCrashed, "The plugin "+c.plugin+" is stopped", said, "Open Plugins", s.publicURL+"/admin/plugins", "💥 ")
	}
}

// tell sends user a notification of k, by mail and on Telegram as they choose, linking
// to linkURL.
func (s *Service) tell(ctx context.Context, user domain.UserID, k Kind, title, said, linkText, linkURL, icon string) {
	telegram, mail, err := s.channels(ctx, user, k)
	if err != nil {
		s.log.Warn(ctx, "Notification skipped", "user_id", user, "kind", k.Key, logging.Err(err))
		return
	}

	mailed := false
	if mail {
		mailed = s.mailUser(ctx, user, k, ports.UserMail{Subject: title, Paragraphs: []string{said}, LinkText: linkText, LinkURL: linkURL}) == nil
	}

	if !telegram {
		return
	}

	var buttons [][]ports.TelegramButton
	if strings.HasPrefix(linkURL, "https://") {
		buttons = [][]ports.TelegramButton{{{Text: linkText, URL: linkURL}}}
	}

	err = s.messenger.MessageUser(ctx, user, ports.UserMessage{HTML: icon + html.EscapeString(said), Buttons: buttons})
	if err != nil && !mailed && !errors.Is(err, domain.ErrTelegramNotLinked) && !errors.Is(err, domain.ErrTelegramUnavailable) {
		s.log.Warn(ctx, "Notification not sent", "user_id", user, "kind", k.Key, logging.Err(err))
	}
}

func (s *Service) joinRequested(ctx context.Context, r joinRequest) {
	if s.admins == nil {
		return
	}

	admins, err := s.admins.Admins(ctx)
	if err != nil {
		s.log.Warn(ctx, "Join request notification skipped", logging.Err(err))
		return
	}

	said := r.email + " asks to join."
	if r.note != "" {
		said += " They say: “" + r.note + "”"
	}

	for _, a := range admins {
		s.tell(ctx, a.ID, JoinRequested, r.email+" asks to join", said, "Open Invites", s.publicURL+"/admin/invites", "🙋 ")
	}
}
