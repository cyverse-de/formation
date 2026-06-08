package apps

import "fmt"

// StatusError represents a non-2xx response from a downstream DE service. The
// raw body is retained for server-side logging but should not be forwarded to
// MCP clients verbatim.
type StatusError struct {
	Service string
	Code    int
	Body    string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s service returned status %d", e.Service, e.Code)
}
