package httpapi

import (
	"context"
	"fmt"
	"html"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/sdk"
)

// pythonIndex lists the Python SDK's wheel this server serves, as pip reads a page of
// links: pip install "polythought[pandas]" --find-links <this page>.
//
//	@Summary	The Python SDK, for pip
//	@ID			pythonSDKIndex
//	@Tags		sdk
//	@Produce	html
//	@Success	200	{string}	string	"a page linking the wheel"
//	@Failure	404	{object}	errorBody	"not_found: this server was built without it"
//	@Router		/sdk/python/ [get]
func (s *Server) pythonIndex(ctx context.Context, c *app.RequestContext) {
	name, _, ok := sdk.PythonWheel()
	if !ok {
		s.fail(ctx, c, fmt.Errorf("%w: this server was built without the Python SDK", domain.ErrNotFound))
		return
	}

	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusOK, "text/html; charset=utf-8", fmt.Appendf(nil,
		"<!DOCTYPE html>\n<html><head><title>polythought</title></head><body>\n<a href=\"%s\">%s</a>\n</body></html>\n",
		html.EscapeString(name), html.EscapeString(name)))
}

// pythonWheel is the Python SDK's wheel, built for this server's version.
//
//	@Summary	The Python SDK's wheel
//	@ID			pythonSDKWheel
//	@Tags		sdk
//	@Produce	octet-stream
//	@Param		file	path		string	true	"The wheel's file name, as the index links it"
//	@Success	200		{file}		binary	"the wheel"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Router		/sdk/python/{file} [get]
func (s *Server) pythonWheel(ctx context.Context, c *app.RequestContext) {
	name, data, ok := sdk.PythonWheel()
	if !ok || c.Param("file") != name {
		s.fail(ctx, c, fmt.Errorf("%w: no such file: the index at /api/v1/sdk/python/ links the wheel", domain.ErrNotFound))
		return
	}

	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	c.Data(http.StatusOK, "application/octet-stream", data)
}
