// Package httpapi serves the JSON API over Hertz. Handlers translate HTTP to service
// calls; authorization decisions stay in the services.
//
// The swag annotations on the handlers generate api/openapi.json (make api-spec), which
// the web client's types are generated from.
//
//	@title			Polythought API
//	@version		1
//	@description	Workflow automation for Polymarket. Errors share one shape: {"error": {"code", "message"}}.
//	@servers.url	/api/v1
//
//	@externalDocs.description	Source
//	@externalDocs.url			https://github.com/lein3000zzz/polythought-go
package httpapi

// Each security scheme sits in its own comment block: swag v2.0.0-rc6 names every scheme
// in a block after the block's first one. Handlers name both schemes, one per line,
// meaning either is accepted. (A comment line starting with an annotation is parsed as
// one, even in prose.)

//	@securityDefinitions.apikey	SessionCookie
//	@in							cookie
//	@name						polythought_session
//	@description				Browser session cookie, set by POST /auth/login; __Host-polythought_session when the app is served over https.

//	@securitydefinitions.bearerauth	APIToken
//	@description					Personal API token: Authorization: Bearer pt_...
