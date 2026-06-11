package handlers

import (
	"bufio"
	"context"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/cyverse-de/formation/internal/apierror"
	"github.com/cyverse-de/formation/internal/auth"
	"github.com/cyverse-de/formation/internal/clients"
)

const metadataHeaderPrefix = "x-datastore-"

// Data serves the /data browse, upload, and delete endpoints.
type Data struct {
	terrain *clients.Terrain
}

// NewData wires the data handler with the terrain client.
func NewData(terrainClient *clients.Terrain) *Data {
	return &Data{terrain: terrainClient}
}

// dataPath extracts the iRODS path from the wildcard route parameter,
// ensuring a leading slash like the Python routes.
func dataPath(c echo.Context) string {
	raw := c.Param("*")
	if decoded, err := url.PathUnescape(raw); err == nil {
		raw = decoded
	}
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	return raw
}

// dataCaller resolves the /data identity: any valid token acts as a user
// under its JWT username, and its own token goes to terrain.
func dataCaller(c echo.Context) (*auth.Caller, error) {
	return auth.UserCaller(auth.GetInfo(c))
}

// avuDelimiter returns the avu_delimiter query parameter, defaulting to ",".
func avuDelimiter(c echo.Context) string {
	if c.QueryParams().Has("avu_delimiter") {
		return c.QueryParam("avu_delimiter")
	}
	return ","
}

// Get serves GET /data/{path}: raw (streamed) file contents with optional
// offset/limit paging, or a JSON directory listing, with optional AVU
// metadata response headers.
//
// @Summary Browse a directory or download a file
// @Description Returns a JSON listing for collections, or streams raw file contents for data objects
// @Description (offset/limit allow partial reads). With include_metadata=true, AVU metadata is returned
// @Description in X-Datastore-{attribute} response headers, with value and units joined by avu_delimiter.
// @Tags Data Store
// @Security BearerAuth
// @Produce json
// @Produce octet-stream
// @Param path path string true "iRODS path (e.g. cyverse/home/username)"
// @Param offset query int false "Starting byte offset when reading a file" default(0)
// @Param limit query int false "Maximum bytes to read; 0 or absent reads to the end"
// @Param include_metadata query bool false "Include AVU metadata as response headers" default(false)
// @Param avu_delimiter query string false "Separator between value and units in metadata headers" default(,)
// @Success 200 {object} map[string]interface{} "Directory listing (JSON) or raw file contents"
// @Failure 403 {object} map[string]interface{} "Access denied"
// @Failure 404 {object} map[string]interface{} "Path not found"
// @Router /data/{path} [get]
func (h *Data) Get(c echo.Context) error {
	irodsPath := dataPath(c)
	caller, err := dataCaller(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()

	st, err := h.statPath(ctx, caller.Token, irodsPath)
	if err != nil {
		return err
	}

	includeMetadata, err := boolQueryParam(c, "include_metadata", false)
	if err != nil {
		return err
	}
	delimiter := avuDelimiter(c)

	if !st.IsDirectory() {
		offset, err := intQueryParam(c, "offset", 0)
		if err != nil {
			return err
		}
		limit, err := intQueryParam(c, "limit", 0)
		if err != nil {
			return err
		}

		handle, err := h.openRange(ctx, caller.Token, irodsPath, offset)
		if err != nil {
			return err
		}
		defer func() { _ = handle.Close() }()
		var reader io.Reader = handle
		if limit > 0 {
			reader = io.LimitReader(handle, int64(limit))
		}

		contentType := mime.TypeByExtension(path.Ext(irodsPath))
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		if includeMetadata {
			h.writeMetadataHeaders(ctx, c, caller.Token, st.ID, delimiter)
		}
		// Unlike the Python version (which buffered whole files in memory),
		// file contents are streamed.
		return c.Stream(http.StatusOK, contentType, reader)
	}

	// It's a collection: paging parameters are ignored, like Python.
	entries, err := h.listEntries(ctx, caller.Token, irodsPath)
	if err != nil {
		return err
	}
	if includeMetadata {
		h.writeMetadataHeaders(ctx, c, caller.Token, st.ID, delimiter)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"path":     irodsPath,
		"type":     TypeCollection,
		"contents": entries,
	})
}

// writeMetadataHeaders adds X-Datastore-{attribute} response headers,
// swallowing lookup errors like the Python metadata helpers. Headers are
// written directly into the header map to preserve the iRODS attribute case.
func (h *Data) writeMetadataHeaders(ctx context.Context, c echo.Context, token, dataID, delimiter string) {
	avus, err := h.pathMetadata(ctx, token, dataID)
	if err != nil {
		return
	}
	headers := c.Response().Header()
	for _, avu := range avus {
		value := avu.Value
		if avu.Units != "" {
			value = avu.Value + delimiter + avu.Units
		}
		headers["X-Datastore-"+avu.Attribute] = []string{value}
	}
}

// Put serves PUT /data/{path}: upload file content, create a directory
// (resource_type=directory), or set AVU metadata from X-Datastore-* headers.
//
// @Summary Upload a file, create a directory, or set metadata
// @Description Send a request body to create or update a file; use resource_type=directory with no
// @Description body to create a collection; send no body to an existing path for a metadata-only
// @Description update. Metadata is supplied via X-Datastore-{attribute} request headers (value and
// @Description units split on avu_delimiter). Swagger UI cannot send arbitrary headers; use curl for
// @Description metadata operations.
// @Tags Data Store
// @Security BearerAuth
// @Accept plain
// @Produce json
// @Param path path string true "iRODS path"
// @Param resource_type query string false "Set to directory to create a collection" Enums(directory)
// @Param replace_metadata query bool false "Replace existing AVUs for the attributes being set instead of adding" default(false)
// @Param avu_delimiter query string false "Separator between value and units in metadata headers" default(,)
// @Param content body string false "Raw file content"
// @Success 200 {object} map[string]interface{} "path, type, and created"
// @Failure 400 {object} map[string]interface{} "Ambiguous operation or upload to a directory"
// @Failure 403 {object} map[string]interface{} "Access denied"
// @Failure 404 {object} map[string]interface{} "Parent directory not found"
// @Router /data/{path} [put]
func (h *Data) Put(c echo.Context) error {
	irodsPath := dataPath(c)
	caller, err := dataCaller(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()

	replaceMetadata, err := boolQueryParam(c, "replace_metadata", false)
	if err != nil {
		return err
	}
	metadata := metadataFromHeaders(c.Request().Header, avuDelimiter(c))

	body, hasContent, err := requestBody(c)
	if err != nil {
		return err
	}

	if hasContent {
		result, err := h.UploadFile(ctx, caller.Token, irodsPath, body, metadata, replaceMetadata)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, result)
	}

	var result map[string]any
	switch _, statErr := h.terrain.Stat(ctx, caller.Token, irodsPath); {
	case statErr == nil:
		// Metadata-only update on an existing file or collection.
		result, err = h.UpdateMetadata(ctx, caller.Token, irodsPath, metadata, replaceMetadata)
	case isNotFound(statErr):
		if c.QueryParam("resource_type") != "directory" {
			return apierror.NewBadRequest("Cannot determine operation: provide file content or type=directory parameter")
		}
		result, err = h.MakeDirectory(ctx, caller.Token, irodsPath, metadata, replaceMetadata)
	default:
		return mapDataError(statErr, irodsPath, "Path")
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, result)
}

func putResult(irodsPath, resultType string, created bool) map[string]any {
	return map[string]any{"path": irodsPath, "type": resultType, "created": created}
}

// requestBody returns the request body and whether it has any content,
// peeking one byte when the length is unknown (chunked encoding) so empty
// bodies are still treated as metadata-only requests.
func requestBody(c echo.Context) (io.Reader, bool, error) {
	req := c.Request()
	switch {
	case req.ContentLength > 0:
		return req.Body, true, nil
	case req.ContentLength == 0:
		return req.Body, false, nil
	default:
		buffered := bufio.NewReader(req.Body)
		if _, err := buffered.Peek(1); err != nil {
			if err == io.EOF {
				return buffered, false, nil
			}
			return nil, false, err
		}
		return buffered, true, nil
	}
}

// metadataFromHeaders parses X-Datastore-{attribute} request headers into
// AVUs, splitting value and units on the delimiter. Attribute names are
// lowercased, matching the Python version (Starlette lowercases all request
// header names). Results are sorted by attribute for deterministic ordering.
func metadataFromHeaders(headers http.Header, delimiter string) []AVU {
	avus := make([]AVU, 0, len(headers))
	for name, values := range headers {
		lower := strings.ToLower(name)
		if !strings.HasPrefix(lower, metadataHeaderPrefix) || len(values) == 0 {
			continue
		}
		attribute := lower[len(metadataHeaderPrefix):]

		// Like a Python dict built in header order, the last value wins.
		value := values[len(values)-1]
		units := ""
		if delimiter != "" {
			if split := strings.SplitN(value, delimiter, 2); len(split) == 2 {
				value, units = split[0], split[1]
			}
		}
		avus = append(avus, AVU{Attribute: attribute, Value: value, Units: units})
	}
	slices.SortFunc(avus, func(a, b AVU) int { return strings.Compare(a.Attribute, b.Attribute) })
	return avus
}

// Delete serves DELETE /data/{path} with recurse and dry_run options.
//
// @Summary Delete a file or directory
// @Description Deletes a data object or collection. Use dry_run=true to preview the outcome without
// @Description deleting. Deleting a non-empty directory requires recurse=true, in both real and
// @Description dry-run mode.
// @Tags Data Store
// @Security BearerAuth
// @Produce json
// @Param path path string true "iRODS path"
// @Param recurse query bool false "Allow deleting non-empty directories" default(false)
// @Param dry_run query bool false "Preview the deletion without executing it" default(false)
// @Success 200 {object} map[string]interface{} "path, type, would_delete, deleted, dry_run, and item_count for recursive non-empty directories"
// @Failure 400 {object} map[string]interface{} "Directory not empty"
// @Failure 403 {object} map[string]interface{} "Access denied"
// @Failure 404 {object} map[string]interface{} "Path not found"
// @Router /data/{path} [delete]
func (h *Data) Delete(c echo.Context) error {
	irodsPath := dataPath(c)
	caller, err := dataCaller(c)
	if err != nil {
		return err
	}
	recurse, err := boolQueryParam(c, "recurse", false)
	if err != nil {
		return err
	}
	dryRun, err := boolQueryParam(c, "dry_run", false)
	if err != nil {
		return err
	}

	result, err := h.DeletePath(c.Request().Context(), caller.Token, irodsPath, recurse, dryRun)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, result)
}
