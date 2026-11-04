package gateway

import "strings"

// Target names for upstream routing.
const (
	TargetIdentity  = "identity"
	TargetWorkspace = "workspace"
	TargetLegacy    = "legacy"
)

const apiPrefix = "/api/v1"

// route maps a public request path to its upstream service and the path the
// upstream expects. The gateway is the only place that knows this mapping.
//
//	/api/v1/auth/*            → identity  (path stripped to /auth/*)
//	/api/v1/organizations/*   → workspace (path stripped to /organizations/*)
//	/api/v1/members/*         → workspace (path stripped to /members/*)
//	/api/v1/invitations/*     → workspace (path stripped to /invitations/*)
//	anything else under /api  → legacy    (full path preserved)
func route(path string) (target, upstreamPath string) {
	if !strings.HasPrefix(path, apiPrefix) {
		return TargetLegacy, path
	}
	rest := strings.TrimPrefix(path, apiPrefix)
	if rest == "" {
		rest = "/"
	}
	trimmed := strings.TrimPrefix(rest, "/")
	switch {
	case trimmed == "auth" || strings.HasPrefix(rest, "/auth/"):
		return TargetIdentity, rest
	case trimmed == "organizations" || strings.HasPrefix(rest, "/organizations/"),
		trimmed == "members" || strings.HasPrefix(rest, "/members/"),
		trimmed == "invitations" || strings.HasPrefix(rest, "/invitations/"):
		return TargetWorkspace, rest
	default:
		return TargetLegacy, path
	}
}
