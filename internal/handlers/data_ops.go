package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"unicode/utf8"

	"github.com/cyverse-de/formation/internal/apierror"
	"github.com/cyverse-de/formation/internal/clients"
)

// Data provides the data-store browse, upload, metadata, and delete
// operations backed by terrain, exposed through the MCP tools.
type Data struct {
	terrain *clients.Terrain
}

// NewData wires the data operations with the terrain client.
func NewData(terrainClient *clients.Terrain) *Data {
	return &Data{terrain: terrainClient}
}

func putResult(irodsPath, resultType string, created bool) map[string]any {
	return map[string]any{"path": irodsPath, "type": resultType, "created": created}
}

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

// BrowseResult is an in-memory directory listing or file read from Browse.
type BrowseResult struct {
	Path       string
	Type       string
	Entries    []Entry
	Content    []byte
	Offset     int   // byte offset the file read started at
	NextOffset int   // byte offset where the next page starts
	FileSize   int64 // total size of the file in bytes
	Binary     bool  // content is not text and should not be rendered
	Truncated  bool  // more file bytes follow the returned window
	Metadata   []clients.MetadataAVU
}

// upstreamStatus returns the HTTP status of an UpstreamError, or 0.
func upstreamStatus(err error) int {
	var upstream *apierror.UpstreamError
	if errors.As(err, &upstream) {
		return upstream.Status
	}
	return 0
}

// upstreamErrorCode returns the error_code from an UpstreamError's JSON body,
// or "". Terrain surfaces data-info error codes with non-obvious HTTP statuses
// (ERR_DOES_NOT_EXIST arrives as a 500), so the code is the reliable signal.
func upstreamErrorCode(err error) string {
	var upstream *apierror.UpstreamError
	if !errors.As(err, &upstream) {
		return ""
	}
	var body struct {
		ErrorCode string `json:"error_code"`
	}
	_ = json.Unmarshal([]byte(upstream.Body), &body)
	return body.ErrorCode
}

// isNotFound reports whether the upstream error means the path does not exist.
func isNotFound(err error) bool {
	return upstreamStatus(err) == http.StatusNotFound || upstreamErrorCode(err) == "ERR_DOES_NOT_EXIST"
}

// isPermissionDenied reports whether the upstream error means the caller
// lacks permission on the path. ERR_NOT_OWNER covers delete (data-info
// requires ownership there, not just a write grant) and ERR_NOT_A_USER covers
// callers whose Keycloak account has no data-store user at all.
func isPermissionDenied(err error) bool {
	switch upstreamErrorCode(err) {
	case "ERR_NOT_READABLE", "ERR_NOT_WRITEABLE", "ERR_NOT_AUTHORIZED", "ERR_NOT_OWNER", "ERR_NOT_A_USER":
		return true
	}
	return upstreamStatus(err) == http.StatusForbidden
}

// mapDataError converts terrain's data errors into the Python-compatible
// formation responses: missing paths become "<what> '<path>' not found" and
// permission failures become "Access denied".
func mapDataError(err error, irodsPath, what string) error {
	switch {
	case isPermissionDenied(err):
		return apierror.NewPermissionDenied("")
	case isNotFound(err):
		return apierror.NewNotFound(what, irodsPath)
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

// responseID extracts the data id from a terrain response: the upload and
// overwrite endpoints nest the stat record under "file", directory creation
// returns it at the top level.
func responseID(response map[string]any) string {
	if file, ok := response["file"].(map[string]any); ok {
		response = file
	}
	id, _ := response["id"].(string)
	return id
}

// pathID returns a data id from the response when present, falling back to a
// stat call for responses that omit it.
func (h *Data) pathID(ctx context.Context, token, irodsPath string, response map[string]any) (string, error) {
	if id := responseID(response); id != "" {
		return id, nil
	}
	st, err := h.statPath(ctx, token, irodsPath)
	if err != nil {
		return "", err
	}
	return st.ID, nil
}

// listEntries reads the full directory listing reshaped to formation entries;
// children sizes the listing request from the directory's stat record.
func (h *Data) listEntries(ctx context.Context, token, irodsPath string, children int) ([]Entry, error) {
	folders, files, err := h.terrain.ListDirectory(ctx, token, irodsPath, children)
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

// looksBinary reports whether a file chunk cannot be retrieved faithfully.
// data-info delivers chunks as JSON strings, replacing unreadable bytes with
// U+FFFD, so any replacement character means bytes were substituted (or the
// file already contains lossy-decoded text — indistinguishable) and the
// byte-offset arithmetic paging depends on no longer matches the file.
// Refusing such content is the only honest answer; rendering it would
// silently skip or repeat file bytes across pages. NUL bytes are valid UTF-8
// and survive the transport, so they signal binary directly. The utf8.Valid
// check is a safety net for any future raw-byte read path.
func looksBinary(content []byte) bool {
	return bytes.IndexByte(content, 0) >= 0 ||
		bytes.Contains(content, []byte("�")) ||
		!utf8.Valid(content)
}

// trimToRuneBoundary cuts content to at most limit bytes without splitting a
// multibyte character.
func trimToRuneBoundary(content []byte, limit int) []byte {
	cut := limit
	for cut > 0 && !utf8.RuneStart(content[cut]) {
		cut--
	}
	return content[:cut]
}

// pathMetadata returns the data item's iRODS AVUs.
func (h *Data) pathMetadata(ctx context.Context, token, dataID string) ([]clients.MetadataAVU, error) {
	avus, _, err := h.terrain.GetMetadata(ctx, token, dataID)
	return avus, err
}

// setMetadata applies AVUs with the previous iRODS semantics: without replace
// the AVUs are simply added alongside the existing ones; with replace, the
// attributes being set lose their existing AVUs first, via terrain's
// set-the-full-listing endpoint.
func (h *Data) setMetadata(ctx context.Context, token, dataID string, metadata []clients.MetadataAVU, replace bool) error {
	if !replace {
		return h.terrain.AddMetadata(ctx, token, dataID, metadata)
	}

	existing, rest, err := h.terrain.GetMetadata(ctx, token, dataID)
	if err != nil {
		return err
	}

	newAttrs := make(map[string]bool, len(metadata))
	for _, avu := range metadata {
		newAttrs[avu.Attr] = true
	}

	merged := make([]clients.MetadataAVU, 0, len(existing)+len(metadata))
	seen := make(map[clients.MetadataAVU]bool, len(existing)+len(metadata))
	for _, avu := range existing {
		if newAttrs[avu.Attr] {
			continue
		}
		merged = append(merged, avu)
		seen[avu] = true
	}
	for _, avu := range metadata {
		if seen[avu] {
			continue
		}
		merged = append(merged, avu)
		seen[avu] = true
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
		entries, err := h.listEntries(ctx, token, irodsPath, st.Children())
		if err != nil {
			return nil, err
		}
		result.Entries = entries
	} else {
		result.Type = TypeDataObject
		// data-info errors on negative seek positions; treat them as 0 like
		// the old download path did.
		offset = max(offset, 0)
		readLimit := maxBytes
		if limit > 0 && limit < readLimit {
			readLimit = limit
		}
		// Over-read by one rune so a character split across the window edge
		// (which data-info would silently drop) can be returned whole; the
		// rune-boundary trim below keeps the page within readLimit.
		content, fileSize, err := h.terrain.ReadChunk(ctx, token, irodsPath, offset, readLimit+utf8.UTFMax-1)
		if err != nil {
			return nil, mapDataError(err, irodsPath, "Path")
		}
		result.Binary = looksBinary(content)
		if !result.Binary && len(content) > readLimit {
			trimmed := trimToRuneBoundary(content, readLimit)
			if len(trimmed) == 0 {
				// The next character is wider than the limit; deliver it
				// whole anyway so paging always advances.
				_, runeLen := utf8.DecodeRune(content)
				trimmed = content[:runeLen]
			}
			content = trimmed
		}
		result.Content = content
		result.Offset = offset
		result.FileSize = fileSize
		// Content is clean UTF-8 here (looksBinary refused anything
		// substituted), so decoded length equals file bytes consumed and the
		// offset arithmetic below is exact.
		result.NextOffset = offset + len(content)
		// More bytes follow when the file extends past the delivered window.
		result.Truncated = int64(result.NextOffset) < fileSize
		// An offset landing inside a multibyte character reads as an empty
		// page (data-info drops the partial rune); advance by one byte so
		// paging re-synchronizes instead of looping on the same offset.
		if result.Truncated && result.NextOffset == offset {
			result.NextOffset++
		}
	}

	// Metadata lookup errors are swallowed so an outage doesn't break reads.
	if includeMetadata {
		if avus, err := h.pathMetadata(ctx, token, st.ID); err == nil {
			result.Metadata = avus
		}
	}
	return result, nil
}

// UploadFile stores file content at irodsPath, creating or overwriting it,
// and optionally sets metadata.
func (h *Data) UploadFile(ctx context.Context, token, irodsPath string, content io.Reader, metadata []clients.MetadataAVU, replace bool) (map[string]any, error) {
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

	case isNotFound(err):
		parent := path.Dir(irodsPath)
		parentStat, err := h.terrain.Stat(ctx, token, parent)
		if err != nil {
			if isNotFound(err) {
				return nil, apierror.NewNotFound("Parent directory", parent)
			}
			return nil, mapDataError(err, parent, "Parent directory")
		}
		if !parentStat.CanWrite() {
			return nil, apierror.NewPermissionDenied("")
		}

		uploaded, err := h.terrain.UploadFile(ctx, token, parent, path.Base(irodsPath), content)
		if err != nil {
			return nil, mapDataError(err, irodsPath, "Path")
		}
		if len(metadata) > 0 {
			id, err := h.pathID(ctx, token, irodsPath, uploaded)
			if err != nil {
				return nil, err
			}
			if err := h.setMetadata(ctx, token, id, metadata, replace); err != nil {
				return nil, mapDataError(err, irodsPath, "Path")
			}
		}
		return putResult(irodsPath, TypeDataObject, true), nil

	default:
		return nil, mapDataError(err, irodsPath, "Path")
	}
}

// UpdateMetadata sets AVUs on an existing file or collection.
func (h *Data) UpdateMetadata(ctx context.Context, token, irodsPath string, metadata []clients.MetadataAVU, replace bool) (map[string]any, error) {
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
func (h *Data) MakeDirectory(ctx context.Context, token, irodsPath string, metadata []clients.MetadataAVU, replace bool) (map[string]any, error) {
	if _, err := h.terrain.Stat(ctx, token, irodsPath); err == nil {
		return h.UpdateMetadata(ctx, token, irodsPath, metadata, replace)
	} else if !isNotFound(err) {
		return nil, mapDataError(err, irodsPath, "Path")
	}

	parent := path.Dir(irodsPath)
	parentStat, err := h.terrain.Stat(ctx, token, parent)
	if err != nil {
		if isNotFound(err) {
			return nil, apierror.NewNotFound("Parent directory", parent)
		}
		return nil, mapDataError(err, parent, "Parent directory")
	}
	if !parentStat.CanWrite() {
		return nil, apierror.NewPermissionDenied("")
	}

	created, err := h.terrain.CreateDirectory(ctx, token, irodsPath)
	if err != nil {
		return nil, mapDataError(err, irodsPath, "Path")
	}
	if len(metadata) > 0 {
		id, err := h.pathID(ctx, token, irodsPath, created)
		if err != nil {
			return nil, err
		}
		if err := h.setMetadata(ctx, token, id, metadata, replace); err != nil {
			return nil, mapDataError(err, irodsPath, "Path")
		}
	}
	return putResult(irodsPath, TypeCollection, true), nil
}

// DeletePath deletes a file or collection, with dry-run and recurse options.
// The dry run reports the same errors a real delete would, except ownership:
// stat cannot distinguish a write grant from ownership, so a write-but-not-own
// caller passes the dry run while the real delete is denied (ERR_NOT_OWNER).
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
