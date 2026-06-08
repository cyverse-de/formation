package mcpserver

import (
	"context"
	"errors"
	"regexp"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cyverse-de/formation/internal/apperr"
	"github.com/cyverse-de/formation/internal/authz"
	"github.com/cyverse-de/formation/internal/datastore"
)

// errUnauthenticated is returned when no identity is present in the request.
// In practice the bearer-token middleware rejects such requests first.
var errUnauthenticated = errors.New("unauthenticated")

// caller returns the authenticated identity for this tool call, or an error if
// none is present. It reads the per-call bearer token from the request's
// RequestExtra (the canonical SDK path), falling back to the context (used by
// tests via authz.ContextWithIdentity).
func caller(ctx context.Context, req *mcp.CallToolRequest) (authz.Identity, error) {
	var id authz.Identity
	if req != nil {
		if extra := req.GetExtra(); extra != nil && extra.TokenInfo != nil {
			id = authz.IdentityFromTokenInfo(extra.TokenInfo)
		}
	}
	if id.DownstreamUsername == "" {
		id = authz.FromContext(ctx)
	}
	if id.DownstreamUsername == "" {
		return authz.Identity{}, errUnauthenticated
	}
	return id, nil
}

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// validateUUID returns a ValidationError if s is not a UUID.
func validateUUID(s, field string) error {
	if !uuidRE.MatchString(s) {
		return apperr.Validation(field, "Invalid "+field+" format")
	}
	return nil
}

// requireData reports an error when the data store is unavailable (e.g. iRODS
// was unreachable at startup).
func (d *Deps) requireData() error {
	if d.Data == nil {
		return apperr.ServiceUnavailable("Data store")
	}
	return nil
}

// toAVUs converts tool metadata inputs to datastore AVUs.
func toAVUs(in []MetaIn) []datastore.AVU {
	if len(in) == 0 {
		return nil
	}
	out := make([]datastore.AVU, 0, len(in))
	for _, m := range in {
		out = append(out, datastore.AVU{Attribute: m.Attribute, Value: m.Value, Units: m.Units})
	}
	return out
}
