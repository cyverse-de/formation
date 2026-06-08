package datastore

import (
	"slices"

	"github.com/cyverse/go-irodsclient/irods/types"
)

// readLevels are the iRODS access levels that grant read access.
var readLevels = []types.IRODSAccessLevelType{
	types.IRODSAccessLevelReadObject,
	types.IRODSAccessLevelModifyObject,
	types.IRODSAccessLevelOwner,
}

// writeLevels are the iRODS access levels that grant write access.
var writeLevels = []types.IRODSAccessLevelType{
	types.IRODSAccessLevelModifyObject,
	types.IRODSAccessLevelOwner,
}

// hasAccess reports whether the user holds any of the required access levels in
// the supplied ACL list. It mirrors the original _user_has_permission: an
// absent match means no access.
func hasAccess(accesses []*types.IRODSAccess, username string, required []types.IRODSAccessLevelType) bool {
	for _, a := range accesses {
		if a.UserName != username {
			continue
		}
		if slices.Contains(required, a.AccessLevel) {
			return true
		}
	}
	return false
}
