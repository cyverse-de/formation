package handlers

import (
	"bufio"
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
	"github.com/cyverse-de/formation/internal/datastore"
)

const metadataHeaderPrefix = "x-datastore-"

// Data serves the /data browse, upload, and delete endpoints.
type Data struct {
	store datastore.Store
}

// NewData wires the data handler with its iRODS store.
func NewData(store datastore.Store) *Data {
	return &Data{store: store}
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

// dataUsername returns the JWT username (preferred_username, then sub).
func dataUsername(c echo.Context) (string, error) {
	return auth.GetInfo(c).Claims.Username()
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
	if !h.store.PathExists(irodsPath) {
		return apierror.NewNotFound("Path", irodsPath)
	}

	username, err := dataUsername(c)
	if err != nil {
		return err
	}
	if !h.store.UserCanRead(username, irodsPath) {
		return apierror.NewPermissionDenied("")
	}

	includeMetadata, err := boolQueryParam(c, "include_metadata", false)
	if err != nil {
		return err
	}
	delimiter := avuDelimiter(c)

	if h.store.FileExists(irodsPath) {
		offset, err := intQueryParam(c, "offset", 0)
		if err != nil {
			return err
		}
		limit, err := intQueryParam(c, "limit", 0)
		if err != nil {
			return err
		}

		handle, err := h.store.OpenFile(irodsPath)
		if err != nil {
			return err
		}
		defer func() { _ = handle.Close() }()
		if offset > 0 {
			if _, err := handle.Seek(int64(offset), io.SeekStart); err != nil {
				return err
			}
		}
		var reader io.Reader = handle
		if limit > 0 {
			reader = io.LimitReader(handle, int64(limit))
		}

		contentType := mime.TypeByExtension(path.Ext(irodsPath))
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		if includeMetadata {
			h.writeMetadataHeaders(c, irodsPath, delimiter)
		}
		// Unlike the Python version (which buffered whole files in memory),
		// file contents are streamed.
		return c.Stream(http.StatusOK, contentType, reader)
	}

	// It's a collection: paging parameters are ignored, like Python.
	entries, err := h.store.ListCollection(irodsPath)
	if err != nil {
		return err
	}
	if includeMetadata {
		h.writeMetadataHeaders(c, irodsPath, delimiter)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"path":     irodsPath,
		"type":     datastore.TypeCollection,
		"contents": entries,
	})
}

// writeMetadataHeaders adds X-Datastore-{attribute} response headers,
// swallowing lookup errors like the Python metadata helpers. Headers are
// written directly into the header map to preserve the iRODS attribute case.
func (h *Data) writeMetadataHeaders(c echo.Context, irodsPath, delimiter string) {
	avus, err := h.store.Metadata(irodsPath)
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
	username, err := dataUsername(c)
	if err != nil {
		return err
	}

	replaceMetadata, err := boolQueryParam(c, "replace_metadata", false)
	if err != nil {
		return err
	}
	metadata := metadataFromHeaders(c.Request().Header, avuDelimiter(c))

	body, hasContent, err := requestBody(c)
	if err != nil {
		return err
	}

	if h.store.PathExists(irodsPath) {
		if !h.store.UserCanWrite(username, irodsPath) {
			return apierror.NewPermissionDenied("")
		}

		if hasContent {
			if h.store.CollectionExists(irodsPath) {
				return apierror.NewBadRequest("Cannot upload file - path is a directory")
			}
			if err := h.store.UploadFile(irodsPath, body); err != nil {
				return err
			}
			if len(metadata) > 0 {
				if err := h.store.SetMetadata(irodsPath, metadata, replaceMetadata); err != nil {
					return err
				}
			}
			return c.JSON(http.StatusOK, putResult(irodsPath, datastore.TypeDataObject, false))
		}

		// Metadata-only update on an existing file or collection.
		resultType := datastore.TypeCollection
		if h.store.FileExists(irodsPath) {
			resultType = datastore.TypeDataObject
		}
		if err := h.store.SetMetadata(irodsPath, metadata, replaceMetadata); err != nil {
			return err
		}
		return c.JSON(http.StatusOK, putResult(irodsPath, resultType, false))
	}

	// Path doesn't exist: create a new file or directory under an existing,
	// writable parent.
	parent := path.Dir(irodsPath)
	if !h.store.PathExists(parent) {
		return apierror.NewNotFound("Parent directory", parent)
	}
	if !h.store.UserCanWrite(username, parent) {
		return apierror.NewPermissionDenied("")
	}

	switch {
	case hasContent:
		if err := h.store.UploadFile(irodsPath, body); err != nil {
			return err
		}
		if len(metadata) > 0 {
			if err := h.store.SetMetadata(irodsPath, metadata, replaceMetadata); err != nil {
				return err
			}
		}
		return c.JSON(http.StatusOK, putResult(irodsPath, datastore.TypeDataObject, true))
	case c.QueryParam("resource_type") == "directory":
		if err := h.store.CreateDirectory(irodsPath); err != nil {
			return err
		}
		if len(metadata) > 0 {
			if err := h.store.SetMetadata(irodsPath, metadata, replaceMetadata); err != nil {
				return err
			}
		}
		return c.JSON(http.StatusOK, putResult(irodsPath, datastore.TypeCollection, true))
	default:
		return apierror.NewBadRequest("Cannot determine operation: provide file content or type=directory parameter")
	}
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
func metadataFromHeaders(headers http.Header, delimiter string) []datastore.AVU {
	avus := make([]datastore.AVU, 0, len(headers))
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
		avus = append(avus, datastore.AVU{Attribute: attribute, Value: value, Units: units})
	}
	slices.SortFunc(avus, func(a, b datastore.AVU) int { return strings.Compare(a.Attribute, b.Attribute) })
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
	username, err := dataUsername(c)
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

	if !h.store.PathExists(irodsPath) {
		return apierror.NewNotFound("Path", irodsPath)
	}
	if !h.store.UserCanWrite(username, irodsPath) {
		return apierror.NewPermissionDenied("")
	}

	result := map[string]any{
		"path":         irodsPath,
		"type":         datastore.TypeDataObject,
		"would_delete": true,
		"deleted":      !dryRun,
		"dry_run":      dryRun,
	}

	isCollection := h.store.CollectionExists(irodsPath)
	if isCollection {
		result["type"] = datastore.TypeCollection
		itemCount, err := h.store.CountCollectionItems(irodsPath)
		if err != nil {
			return err
		}
		// Unlike the Python version, the dry run reports the same error a
		// real delete would, so the preview matches the outcome.
		if !recurse && itemCount > 0 {
			return apierror.NewBadRequest("Directory not empty. Use recurse=true to delete non-empty directories.")
		}
		if recurse && itemCount > 0 {
			result["item_count"] = itemCount
		}
	}

	if !dryRun {
		if isCollection {
			err = h.store.DeleteDirectory(irodsPath, recurse)
		} else {
			err = h.store.DeleteFile(irodsPath)
		}
		if err != nil {
			return err
		}
	}
	return c.JSON(http.StatusOK, result)
}
