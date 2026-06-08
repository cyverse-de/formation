package datastore

import "github.com/cyverse/go-irodsclient/irods/types"

// AVU is an iRODS attribute-value-units metadata triple.
type AVU struct {
	Attribute string
	Value     string
	Units     string
}

// formatMetadataHeaders renders AVU metadata as X-Datastore-{attribute} headers.
// When units are present, value and units are joined with the delimiter,
// matching the original _format_metadata_as_headers.
func formatMetadataHeaders(metas []*types.IRODSMeta, delimiter string) map[string]string {
	if delimiter == "" {
		delimiter = ","
	}
	headers := make(map[string]string, len(metas))
	for _, m := range metas {
		value := m.Value
		if m.Units != "" {
			value = m.Value + delimiter + m.Units
		}
		headers["X-Datastore-"+m.Name] = value
	}
	return headers
}
