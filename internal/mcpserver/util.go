package mcpserver

import (
	"context"
	"errors"
	"regexp"

	"github.com/cyverse-de/formation/internal/apperr"
	"github.com/cyverse-de/formation/internal/authz"
	"github.com/cyverse-de/formation/internal/datastore"
)

// errUnauthenticated is returned when no identity is present in the context.
// In practice the bearer-token middleware rejects such requests first.
var errUnauthenticated = errors.New("unauthenticated")

// caller returns the authenticated identity, or an error if none is present.
func caller(ctx context.Context) (authz.Identity, error) {
	id := authz.FromContext(ctx)
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
