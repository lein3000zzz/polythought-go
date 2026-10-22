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
	Label: "A workflow is paused",
	Description: "One of your active workflows uses a plugin an admin disabled or removed, or no longer lets you use, " +
		"so it was paused. Turn it on again once it can run.",
	Default: true, EmailDefault: true,
}

var PluginCrashed = Kind{
	Key: "plugin_crashed", Group: groupAdmin,
	Label: "A plugin keeps crashing",
	Description: "A plugin crashed 5 times in 10 minutes, or would not start, and is stopped until an admin starts it " +
		"again on the Plugins page.",
	Default: true, EmailDefault: true, Admins: true,
}

// AdminLister lists the instance's admins.
type AdminLister interface {
	Admins(ctx context.Context) ([]domain.User, error)
}

// WithAdmins tells admins about plugins that crashed for good (D320).
func WithAdmins(a AdminLister) Option {
	return func(s *Service) { s.admins = a }
}

type pausedWorkflow struct {
	owner    domain.UserID
	workflow domain.Workflow
	why      string
}

type crashedPlugin struct {
	plugin string
	why    string
}

// WorkflowPaused queues telling a workflow's owner why it was paused (D319). It never
// blocks: when the queue is full, it goes untold.
func (s *Service) WorkflowPaused(owner domain.UserID, w domain.Workflow, why string) {
	select {
	case s.paused <- pausedWorkflow{owner: owner, workflow: w, why: why}:
	default:
		s.log.Warn(context.Background(), "Paused workflow notification dropped: too many at once", "workflow_id", w.ID)
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

func (s *Service) workflowPaused(ctx context.Context, p pausedWorkflow) {
	said := "Your workflow “" + p.workflow.Name + "” was paused: " + p.why + ". Turn it on again once it can run."

	s.tell(ctx, p.owner, WorkflowPaused, "Your workflow “"+p.workflow.Name+"” was paused", said,
		"Open the workflow", s.publicURL+"/workflows/"+string(p.workflow.ID), "⏸ ")
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
