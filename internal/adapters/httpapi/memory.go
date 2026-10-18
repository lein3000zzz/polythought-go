package httpapi

import (
	"context"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// MemoryService shows what a workflow's nodes remember to its owner, and forgets it
// (D137).
type MemoryService interface {
	Count(ctx context.Context, p auth.Principal, id domain.WorkflowID, node domain.NodeID) (int, error)
	Forget(ctx context.Context, p auth.Principal, id domain.WorkflowID, node domain.NodeID) error
}

type nodeMemoryDTO struct {
	// Count is how many keys the node remembers now, like the items Only new has seen.
	Count int `json:"count"`
} // @name NodeMemory

// getNodeMemory says how much a node remembers across runs (D121).
//
//	@Summary	Node memory
//	@ID			getNodeMemory
//	@Tags		workflows
//	@Param		id		path		string	true	"Workflow ID"
//	@Param		node	path		string	true	"Node ID"
//	@Success	200		{object}	nodeMemoryDTO
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/workflows/{id}/nodes/{node}/memory [get]
func (s *Server) getNodeMemory(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	n, err := s.memory.Count(ctx, p, domain.WorkflowID(c.Param("id")), domain.NodeID(c.Param("node")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, nodeMemoryDTO{Count: n})
}

// forgetNodeMemory clears what a node remembers, for a fresh start (D137).
//
//	@Summary	Forget a node's memory
//	@ID			forgetNodeMemory
//	@Tags		workflows
//	@Param		id		path	string	true	"Workflow ID"
//	@Param		node	path	string	true	"Node ID"
//	@Success	204
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/workflows/{id}/nodes/{node}/memory [delete]
func (s *Server) forgetNodeMemory(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.memory.Forget(ctx, p, domain.WorkflowID(c.Param("id")), domain.NodeID(c.Param("node"))); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
