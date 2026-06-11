package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path"

	"github.com/cyverse-de/formation/internal/apierror"
	"github.com/cyverse-de/formation/internal/clients"
)

// This file holds the echo-free bodies of the /data operations so the MCP
// tools can call the same logic in-process. The Echo handlers wrap these and
// must keep their response shapes byte-compatible with the Python API.

// Entry types reported in directory listings and delete results.
const (
	TypeCollection = "collection"
	TypeDataObject = "data_object"
)

// Entry is one item in a collection listing.
type Entry struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// AVU is one iRODS attribute-value-units metadata triple.
type AVU struct {
	Attribute string
	Value     string
	Units     string
}

// BrowseResult is an in-memory directory listing or file read from Browse.
type BrowseResult struct {
	Path      string
	Type      string
	Entries   []Entry
	Content   []byte
	Truncated bool // more file bytes were available than returned
	Metadata  []AVU
}

// upstreamStatus returns the HTTP status of an UpstreamError, or 0.
func upstreamStatus(err error) int {
	var upstream *apierror.UpstreamError
	if errors.As(err, &upstream) {
		return upstream.Status
	}
	return 0
}

// mapDataError converts terrain's data errors into the Python-compatible
// formation responses: 404 becomes "<what> '<path>' not found" and 403
// becomes "Access denied".
func mapDataError(err error, irodsPath, what string) error {
	switch upstreamStatus(err) {
	case http.StatusNotFound:
		return apierror.NewNotFound(what, irodsPath)
	case http.StatusForbidden:
		return apierror.NewPermissionDenied("")
	}
	return err
}

// statPath fetches a path's stat record with formation error mapping.
func (h *Data) statPath(ctx context.Context, token, irodsPath string) (*clients.StatInfo, error) {
	info, err := h.terrain.Stat(ctx, token, irodsPath)
	if err != nil {
		return nil, mapDataError(err, irodsPath, "Path")
	}
	return info, nil
}

// listEntries reads the full directory listing reshaped to formation entries.
func (h *Data) listEntries(ctx context.Context, token, irodsPath string) ([]Entry, error) {
	folders, files, err := h.terrain.ListDirectory(ctx, token, irodsPath)
	if err != nil {
		return nil, mapDataError(err, irodsPath, "Path")
	}
	entries := make([]Entry, 0, len(folders)+len(files))
	for _, name := range folders {
		entries = append(entries, Entry{Name: name, Type: TypeCollection})
	}
	for _, name := range files {
		entries = append(entries, Entry{Name: name, Type: TypeDataObject})
	}
	return entries, nil
}

// openRange opens the file stream and discards offset bytes, emulating the
// previous seek-based partial reads (terrain's download has no range support).
func (h *Data) openRange(ctx context.Context, token, irodsPath string, offset int) (io.ReadCloser, error) {
	reader, err := h.terrain.DownloadFile(ctx, token, irodsPath)
	if err != nil {
		return nil, mapDataError(err, irodsPath, "Path")
	}
	if offset > 0 {
		if _, err := io.CopyN(io.Discard, reader, int64(offset)); err != nil && err != io.EOF {
			_ = reader.Close()
			return nil, err
		}
	}
	return reader, nil
}

// pathMetadata returns the data item's iRODS AVUs reshaped to formation AVUs.
func (h *Data) pathMetadata(ctx context.Context, token, dataID string) ([]AVU, error) {
	avus, _, err := h.terrain.GetMetadata(ctx, token, dataID)
	if err != nil {
		return nil, err
	}
	converted := make([]AVU, 0, len(avus))
	for _, avu := range avus {
		converted = append(converted, AVU{Attribute: avu.Attr, Value: avu.Value, Units: avu.Unit})
	}
	return converted, nil
}

// setMetadata reproduces the previous per-attribute iRODS semantics on top of
// terrain's set-the-full-listing endpoint: with replace, the attributes being
// set lose their existing AVUs first; without it, the new AVUs are added
// alongside the existing ones.
func (h *Data) setMetadata(ctx context.Context, token, dataID string, metadata []AVU, replace bool) error {
	existing, rest, err := h.terrain.GetMetadata(ctx, token, dataID)
	if err != nil {
		return err
	}

	newAttrs := make(map[string]bool, len(metadata))
	for _, avu := range metadata {
		newAttrs[avu.Attribute] = true
	}

	merged := make([]clients.MetadataAVU, 0, len(existing)+len(metadata))
	seen := make(map[clients.MetadataAVU]bool, len(existing)+len(metadata))
	for _, avu := range existing {
		if replace && newAttrs[avu.Attr] {
			continue
		}
		merged = append(merged, avu)
		seen[avu] = true
	}
	for _, avu := range metadata {
		converted := clients.MetadataAVU{Attr: avu.Attribute, Value: avu.Value, Unit: avu.Units}
		if seen[converted] {
			continue
		}
		merged = append(merged, converted)
		seen[converted] = true
	}

	return h.terrain.SetMetadata(ctx, token, dataID, merged, rest)
}

// Browse lists a collection or reads a data object into memory. File reads
// honor offset/limit (limit 0 means unlimited) but never exceed maxBytes.
func (h *Data) Browse(ctx context.Context, token, irodsPath string, offset, limit int, includeMetadata bool, maxBytes int) (*BrowseResult, error) {
	st, err := h.statPath(ctx, token, irodsPath)
	if err != nil {
		return nil, err
	}

	result := &BrowseResult{Path: irodsPath}
	if st.IsDirectory() {
		result.Type = TypeCollection
		entries, err := h.listEntries(ctx, token, irodsPath)
		if err != nil {
			return nil, err
		}
		result.Entries = entries
	} else {
		result.Type = TypeDataObject
		reader, err := h.openRange(ctx, token, irodsPath, offset)
		if err != nil {
			return nil, err
		}
		defer func() { _ = reader.Close() }()

		readLimit := maxBytes
		if limit > 0 && limit < readLimit {
			readLimit = limit
		}
		// Read one extra byte to detect truncation without slurping the rest.
		content, err := io.ReadAll(io.LimitReader(reader, int64(readLimit)+1))
		if err != nil {
			return nil, err
		}
		if len(content) > readLimit {
			content = content[:readLimit]
			result.Truncated = true
		}
		result.Content = content
	}

	// Metadata lookup errors are swallowed, like the REST metadata headers.
	if includeMetadata {
		if avus, err := h.pathMetadata(ctx, token, st.ID); err == nil {
			result.Metadata = avus
		}
	}
	return result, nil
}

// UploadFile stores file content at irodsPath, creating or overwriting it,
// and optionally sets metadata.
func (h *Data) UploadFile(ctx context.Context, token, irodsPath string, content io.Reader, metadata []AVU, replace bool) (map[string]any, error) {
	st, err := h.terrain.Stat(ctx, token, irodsPath)
	switch {
	case err == nil:
		if !st.CanWrite() {
			return nil, apierror.NewPermissionDenied("")
		}
		if st.IsDirectory() {
			return nil, apierror.NewBadRequest("Cannot upload file - path is a directory")
		}
		if _, err := h.terrain.OverwriteFile(ctx, token, irodsPath, content); err != nil {
			return nil, mapDataError(err, irodsPath, "Path")
		}
		if len(metadata) > 0 {
			if err := h.setMetadata(ctx, token, st.ID, metadata, replace); err != nil {
				return nil, mapDataError(err, irodsPath, "Path")
			}
		}
		return putResult(irodsPath, TypeDataObject, false), nil

	case upstreamStatus(err) == http.StatusNotFound:
		parent := path.Dir(irodsPath)
		parentStat, err := h.terrain.Stat(ctx, token, parent)
		if err != nil {
			if upstreamStatus(err) == http.StatusNotFound {
				return nil, apierror.NewNotFound("Parent directory", parent)
			}
			return nil, mapDataError(err, parent, "Parent directory")
		}
		if !parentStat.CanWrite() {
			return nil, apierror.NewPermissionDenied("")
		}

		if _, err := h.terrain.UploadFile(ctx, token, parent, path.Base(irodsPath), content); err != nil {
			return nil, mapDataError(err, irodsPath, "Path")
		}
		if len(metadata) > 0 {
			created, err := h.statPath(ctx, token, irodsPath)
			if err != nil {
				return nil, err
			}
			if err := h.setMetadata(ctx, token, created.ID, metadata, replace); err != nil {
				return nil, mapDataError(err, irodsPath, "Path")
			}
		}
		return putResult(irodsPath, TypeDataObject, true), nil

	default:
		return nil, mapDataError(err, irodsPath, "Path")
	}
}

// UpdateMetadata sets AVUs on an existing file or collection.
func (h *Data) UpdateMetadata(ctx context.Context, token, irodsPath string, metadata []AVU, replace bool) (map[string]any, error) {
	st, err := h.statPath(ctx, token, irodsPath)
	if err != nil {
		return nil, err
	}
	if !st.CanWrite() {
		return nil, apierror.NewPermissionDenied("")
	}

	resultType := TypeCollection
	if !st.IsDirectory() {
		resultType = TypeDataObject
	}
	if err := h.setMetadata(ctx, token, st.ID, metadata, replace); err != nil {
		return nil, mapDataError(err, irodsPath, "Path")
	}
	return putResult(irodsPath, resultType, false), nil
}

// MakeDirectory creates a collection with optional metadata. An existing
// path becomes a metadata-only update, matching the PUT semantics.
func (h *Data) MakeDirectory(ctx context.Context, token, irodsPath string, metadata []AVU, replace bool) (map[string]any, error) {
	if _, err := h.terrain.Stat(ctx, token, irodsPath); err == nil {
		return h.UpdateMetadata(ctx, token, irodsPath, metadata, replace)
	} else if upstreamStatus(err) != http.StatusNotFound {
		return nil, mapDataError(err, irodsPath, "Path")
	}

	parent := path.Dir(irodsPath)
	parentStat, err := h.terrain.Stat(ctx, token, parent)
	if err != nil {
		if upstreamStatus(err) == http.StatusNotFound {
			return nil, apierror.NewNotFound("Parent directory", parent)
		}
		return nil, mapDataError(err, parent, "Parent directory")
	}
	if !parentStat.CanWrite() {
		return nil, apierror.NewPermissionDenied("")
	}

	if err := h.terrain.CreateDirectory(ctx, token, irodsPath); err != nil {
		return nil, mapDataError(err, irodsPath, "Path")
	}
	if len(metadata) > 0 {
		created, err := h.statPath(ctx, token, irodsPath)
		if err != nil {
			return nil, err
		}
		if err := h.setMetadata(ctx, token, created.ID, metadata, replace); err != nil {
			return nil, mapDataError(err, irodsPath, "Path")
		}
	}
	return putResult(irodsPath, TypeCollection, true), nil
}

// DeletePath deletes a file or collection, with dry-run and recurse options.
// The dry run reports the same error a real delete would.
func (h *Data) DeletePath(ctx context.Context, token, irodsPath string, recurse, dryRun bool) (map[string]any, error) {
	st, err := h.statPath(ctx, token, irodsPath)
	if err != nil {
		return nil, err
	}
	if !st.CanWrite() {
		return nil, apierror.NewPermissionDenied("")
	}

	result := map[string]any{
		"path":         irodsPath,
		"type":         TypeDataObject,
		"would_delete": true,
		"deleted":      !dryRun,
		"dry_run":      dryRun,
	}

	if st.IsDirectory() {
		result["type"] = TypeCollection
		itemCount := st.Children()
		if !recurse && itemCount > 0 {
			return nil, apierror.NewBadRequest("Directory not empty. Use recurse=true to delete non-empty directories.")
		}
		if recurse && itemCount > 0 {
			result["item_count"] = itemCount
		}
	}

	if !dryRun {
		if err := h.terrain.DeletePaths(ctx, token, []string{irodsPath}); err != nil {
			return nil, mapDataError(err, irodsPath, "Path")
		}
	}
	return result, nil
}
