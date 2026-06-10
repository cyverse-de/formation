package handlers

import (
	"io"
	"path"

	"github.com/cyverse-de/formation/internal/apierror"
	"github.com/cyverse-de/formation/internal/datastore"
)

// This file holds the echo-free bodies of the /data operations so the MCP
// tools can call the same logic in-process. The Echo handlers wrap these and
// must keep their response shapes byte-compatible with the Python API.

// BrowseResult is an in-memory directory listing or file read from Browse.
type BrowseResult struct {
	Path      string
	Type      string
	Entries   []datastore.Entry
	Content   []byte
	Truncated bool // more file bytes were available than returned
	Metadata  []datastore.AVU
}

// Browse lists a collection or reads a data object into memory. File reads
// honor offset/limit (limit 0 means unlimited) but never exceed maxBytes.
func (h *Data) Browse(username, irodsPath string, offset, limit int, includeMetadata bool, maxBytes int) (*BrowseResult, error) {
	if !h.store.PathExists(irodsPath) {
		return nil, apierror.NewNotFound("Path", irodsPath)
	}
	if !h.store.UserCanRead(username, irodsPath) {
		return nil, apierror.NewPermissionDenied("")
	}

	result := &BrowseResult{Path: irodsPath}
	if h.store.FileExists(irodsPath) {
		result.Type = datastore.TypeDataObject
		handle, err := h.store.OpenFile(irodsPath)
		if err != nil {
			return nil, err
		}
		defer func() { _ = handle.Close() }()
		if offset > 0 {
			if _, err := handle.Seek(int64(offset), io.SeekStart); err != nil {
				return nil, err
			}
		}

		readLimit := maxBytes
		if limit > 0 && limit < readLimit {
			readLimit = limit
		}
		// Read one extra byte to detect truncation without slurping the rest.
		content, err := io.ReadAll(io.LimitReader(handle, int64(readLimit)+1))
		if err != nil {
			return nil, err
		}
		if len(content) > readLimit {
			content = content[:readLimit]
			result.Truncated = true
		}
		result.Content = content
	} else {
		result.Type = datastore.TypeCollection
		entries, err := h.store.ListCollection(irodsPath)
		if err != nil {
			return nil, err
		}
		result.Entries = entries
	}

	// Metadata lookup errors are swallowed, like the REST metadata headers.
	if includeMetadata {
		if avus, err := h.store.Metadata(irodsPath); err == nil {
			result.Metadata = avus
		}
	}
	return result, nil
}

// UploadFile stores file content at irodsPath, creating or overwriting it,
// and optionally sets metadata.
func (h *Data) UploadFile(username, irodsPath string, content io.Reader, metadata []datastore.AVU, replace bool) (map[string]any, error) {
	created := !h.store.PathExists(irodsPath)
	if created {
		parent := path.Dir(irodsPath)
		if !h.store.PathExists(parent) {
			return nil, apierror.NewNotFound("Parent directory", parent)
		}
		if !h.store.UserCanWrite(username, parent) {
			return nil, apierror.NewPermissionDenied("")
		}
	} else {
		if !h.store.UserCanWrite(username, irodsPath) {
			return nil, apierror.NewPermissionDenied("")
		}
		if h.store.CollectionExists(irodsPath) {
			return nil, apierror.NewBadRequest("Cannot upload file - path is a directory")
		}
	}

	if err := h.store.UploadFile(irodsPath, content); err != nil {
		return nil, err
	}
	if len(metadata) > 0 {
		if err := h.store.SetMetadata(irodsPath, metadata, replace); err != nil {
			return nil, err
		}
	}
	return putResult(irodsPath, datastore.TypeDataObject, created), nil
}

// UpdateMetadata sets AVUs on an existing file or collection.
func (h *Data) UpdateMetadata(username, irodsPath string, metadata []datastore.AVU, replace bool) (map[string]any, error) {
	if !h.store.PathExists(irodsPath) {
		return nil, apierror.NewNotFound("Path", irodsPath)
	}
	if !h.store.UserCanWrite(username, irodsPath) {
		return nil, apierror.NewPermissionDenied("")
	}

	resultType := datastore.TypeCollection
	if h.store.FileExists(irodsPath) {
		resultType = datastore.TypeDataObject
	}
	if err := h.store.SetMetadata(irodsPath, metadata, replace); err != nil {
		return nil, err
	}
	return putResult(irodsPath, resultType, false), nil
}

// MakeDirectory creates a collection with optional metadata. An existing
// path becomes a metadata-only update, matching the PUT semantics.
func (h *Data) MakeDirectory(username, irodsPath string, metadata []datastore.AVU, replace bool) (map[string]any, error) {
	if h.store.PathExists(irodsPath) {
		return h.UpdateMetadata(username, irodsPath, metadata, replace)
	}

	parent := path.Dir(irodsPath)
	if !h.store.PathExists(parent) {
		return nil, apierror.NewNotFound("Parent directory", parent)
	}
	if !h.store.UserCanWrite(username, parent) {
		return nil, apierror.NewPermissionDenied("")
	}

	if err := h.store.CreateDirectory(irodsPath); err != nil {
		return nil, err
	}
	if len(metadata) > 0 {
		if err := h.store.SetMetadata(irodsPath, metadata, replace); err != nil {
			return nil, err
		}
	}
	return putResult(irodsPath, datastore.TypeCollection, true), nil
}

// DeletePath deletes a file or collection, with dry-run and recurse options.
// The dry run reports the same error a real delete would.
func (h *Data) DeletePath(username, irodsPath string, recurse, dryRun bool) (map[string]any, error) {
	if !h.store.PathExists(irodsPath) {
		return nil, apierror.NewNotFound("Path", irodsPath)
	}
	if !h.store.UserCanWrite(username, irodsPath) {
		return nil, apierror.NewPermissionDenied("")
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
			return nil, err
		}
		if !recurse && itemCount > 0 {
			return nil, apierror.NewBadRequest("Directory not empty. Use recurse=true to delete non-empty directories.")
		}
		if recurse && itemCount > 0 {
			result["item_count"] = itemCount
		}
	}

	if !dryRun {
		var err error
		if isCollection {
			err = h.store.DeleteDirectory(irodsPath, recurse)
		} else {
			err = h.store.DeleteFile(irodsPath)
		}
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}
