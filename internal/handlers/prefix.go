package handlers

import (
	"strings"

	"github.com/labstack/echo/v4"
)

// StripPathPrefix removes the configured path prefix from incoming request
// paths before routing, mirroring FastAPI's root_path semantics: the gateway
// forwards requests with the prefix intact (e.g. /formation/apps), while
// in-cluster clients call the bare paths, and both must resolve to the same
// routes. Register it with e.Pre so it runs before the router.
func StripPathPrefix(prefix string) echo.MiddlewareFunc {
	prefix = strings.TrimSuffix(prefix, "/")
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if prefix == "" || prefix == "/" {
				return next(c)
			}
			url := c.Request().URL
			url.Path = stripped(url.Path, prefix)
			if url.RawPath != "" {
				url.RawPath = stripped(url.RawPath, prefix)
			}
			return next(c)
		}
	}
}

func stripped(path, prefix string) string {
	switch {
	case path == prefix:
		return "/"
	case strings.HasPrefix(path, prefix+"/"):
		return strings.TrimPrefix(path, prefix)
	default:
		return path
	}
}
