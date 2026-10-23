package httpapi

// RouteScopes are the routes routeScopes lists, as "METHOD /path".
func RouteScopes() []string {
	out := make([]string, 0, len(routeScopes))
	for k := range routeScopes {
		out = append(out, k)
	}

	return out
}
