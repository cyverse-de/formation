package clients

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/url"
	gopath "path"
	"strconv"

	"github.com/cyverse-de/formation/internal/apierror"
)

// This file holds the terrain client methods behind the /data endpoints:
// stat, directory listings, file transfer, metadata, and deletion.

// StatInfo is the subset of terrain's stat response the data handlers use.
type StatInfo struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Permission string `json:"permission"`
	FileCount  int    `json:"file-count"`
	DirCount   int    `json:"dir-count"`
}

// IsDirectory reports whether the stat record describes a directory.
func (s *StatInfo) IsDirectory() bool {
	return s.Type == "dir"
}

// CanWrite reports whether the caller's effective permission allows writes.
func (s *StatInfo) CanWrite() bool {
	return s.Permission == "write" || s.Permission == "own"
}

// Children returns the number of direct members of a directory.
func (s *StatInfo) Children() int {
	return s.FileCount + s.DirCount
}

// Stat returns terrain's stat record for one path. Missing paths error with
// ERR_DOES_NOT_EXIST; paths the caller cannot read come back as a successful
// response with the path omitted, which is surfaced as a permission error.
func (t *Terrain) Stat(ctx context.Context, token, path string) (*StatInfo, error) {
	data, err := doJSON(ctx, t.client, http.MethodPost,
		endpoint(t.base, nil, "secured", "filesystem", "stat"), token,
		map[string]any{"paths": []string{path}})
	if err != nil {
		return nil, err
	}

	var response struct {
		Paths map[string]*StatInfo `json:"paths"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("decoding stat response for %s: %w", path, err)
	}
	info := response.Paths[path]
	if info == nil {
		// The path exists (missing paths error instead) but data-info filtered
		// it out of the listing because the caller cannot read it.
		return nil, &apierror.UpstreamError{Status: http.StatusForbidden, Body: `{"error_code":"ERR_NOT_READABLE"}`}
	}
	return info, nil
}

// listDirectoryPageSize is the page size used to walk a full directory listing.
const listDirectoryPageSize = 1000

// ListDirectory returns the names of a directory's subdirectories and files,
// paging through terrain's listing endpoint until the whole directory is read.
// sizeHint (e.g. the stat record's child count) sizes the first request so a
// directory of known size is fetched in one round trip.
func (t *Terrain) ListDirectory(ctx context.Context, token, path string, sizeHint int) (folders, files []string, err error) {
	folders = make([]string, 0, sizeHint)
	files = make([]string, 0, sizeHint)
	for fetched := 0; ; {
		limit := max(listDirectoryPageSize, sizeHint-fetched)
		query := url.Values{
			"path":   {path},
			"limit":  {strconv.Itoa(limit)},
			"offset": {strconv.Itoa(fetched)},
		}
		data, err := doJSON(ctx, t.client, http.MethodGet,
			endpoint(t.base, query, "secured", "filesystem", "paged-directory"), token, nil)
		if err != nil {
			return nil, nil, err
		}

		var page struct {
			Total   int `json:"total"`
			Folders []struct {
				Label string `json:"label"`
			} `json:"folders"`
			Files []struct {
				Label string `json:"label"`
			} `json:"files"`
		}
		if err := json.Unmarshal(data, &page); err != nil {
			return nil, nil, fmt.Errorf("decoding directory listing for %s: %w", path, err)
		}
		for _, folder := range page.Folders {
			folders = append(folders, folder.Label)
		}
		for _, file := range page.Files {
			files = append(files, file.Label)
		}

		pageSize := len(page.Folders) + len(page.Files)
		fetched += pageSize
		if fetched >= page.Total || pageSize == 0 {
			return folders, files, nil
		}
	}
}

// doStream executes a request with the caller's bearer token on the streaming
// client, mapping non-2xx responses to UpstreamError like doJSON.
func (t *Terrain) doStream(req *http.Request, token string) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := t.stream.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return nil, &apierror.UpstreamError{Status: resp.StatusCode, Body: string(body)}
	}
	return resp, nil
}

// DownloadFile streams a file's raw contents; the caller must close the reader.
func (t *Terrain) DownloadFile(ctx context.Context, token, path string) (io.ReadCloser, error) {
	query := url.Values{"path": {path}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		endpoint(t.base, query, "secured", "fileio", "download"), nil)
	if err != nil {
		return nil, err
	}
	resp, err := t.doStream(req, token)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// UploadFile creates a new file named filename in the destination directory.
// Terrain answers 409 when the file already exists.
func (t *Terrain) UploadFile(ctx context.Context, token, destDir, filename string, content io.Reader) (map[string]any, error) {
	query := url.Values{"dest": {destDir}}
	return t.uploadMultipart(ctx, endpoint(t.base, query, "secured", "fileio", "upload"), token, filename, content)
}

// OverwriteFile replaces the contents of the existing file at path.
func (t *Terrain) OverwriteFile(ctx context.Context, token, path string, content io.Reader) (map[string]any, error) {
	query := url.Values{"dest": {path}}
	return t.uploadMultipart(ctx, endpoint(t.base, query, "secured", "fileio", "overwrite"), token, gopath.Base(path), content)
}

// uploadMultipart streams content as the "file" part of a multipart POST.
func (t *Terrain) uploadMultipart(ctx context.Context, rawurl, token, filename string, content io.Reader) (map[string]any, error) {
	pipeReader, pipeWriter := io.Pipe()
	writer := multipart.NewWriter(pipeWriter)
	go func() {
		part, err := writer.CreateFormFile("file", filename)
		if err != nil {
			pipeWriter.CloseWithError(err)
			return
		}
		if _, err := io.Copy(part, content); err != nil {
			pipeWriter.CloseWithError(err)
			return
		}
		pipeWriter.CloseWithError(writer.Close())
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawurl, pipeReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := t.doStream(req, token)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return decodeMap(data, rawurl)
}

// CreateDirectory creates a directory, along with any missing intermediates,
// returning terrain's stat record for it (including the data id).
func (t *Terrain) CreateDirectory(ctx context.Context, token, path string) (map[string]any, error) {
	data, err := doJSON(ctx, t.client, http.MethodPost,
		endpoint(t.base, nil, "secured", "filesystem", "directory", "create"), token,
		map[string]any{"path": path})
	if err != nil {
		return nil, err
	}
	return decodeMap(data, "directory create")
}

// MetadataAVU is one iRODS AVU in terrain's metadata payloads.
type MetadataAVU struct {
	Attr  string `json:"attr"`
	Value string `json:"value"`
	Unit  string `json:"unit"`
}

// GetMetadata returns a data item's iRODS AVUs plus the rest of the metadata
// response (the metadata-service AVUs), which SetMetadata must echo back so
// terrain's set-the-full-listing semantics leave the template metadata intact.
func (t *Terrain) GetMetadata(ctx context.Context, token, dataID string) ([]MetadataAVU, map[string]any, error) {
	u := endpoint(t.base, nil, "secured", "filesystem", dataID, "metadata")
	data, err := doJSON(ctx, t.client, http.MethodGet, u, token, nil)
	if err != nil {
		return nil, nil, err
	}

	var parsed struct {
		IRODSAVUs []MetadataAVU `json:"irods-avus"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, nil, fmt.Errorf("decoding metadata for %s: %w", dataID, err)
	}
	rest, err := decodeMap(data, u)
	if err != nil {
		return nil, nil, err
	}
	delete(rest, "irods-avus")
	delete(rest, "path")
	return parsed.IRODSAVUs, rest, nil
}

// SetMetadata sets the complete list of (non-administrative) iRODS AVUs on a
// data item. rest must be the remainder of a GetMetadata response so the
// metadata-service AVUs survive the set operation.
func (t *Terrain) SetMetadata(ctx context.Context, token, dataID string, avus []MetadataAVU, rest map[string]any) error {
	body := make(map[string]any, len(rest)+1)
	maps.Copy(body, rest)
	body["irods-avus"] = avus

	_, err := doJSON(ctx, t.client, http.MethodPost,
		endpoint(t.base, nil, "secured", "filesystem", dataID, "metadata"), token, body)
	return err
}

// AddMetadata associates additional iRODS AVUs with a data item without
// touching the existing ones; exact duplicates are ignored upstream.
func (t *Terrain) AddMetadata(ctx context.Context, token, dataID string, avus []MetadataAVU) error {
	_, err := doJSON(ctx, t.client, http.MethodPost,
		endpoint(t.base, nil, "secured", "filesystem", dataID, "metadata", "add"), token,
		map[string]any{"irods-avus": avus})
	return err
}

// DeletePaths moves the paths to the trash; terrain's delete is always
// recursive for directories.
func (t *Terrain) DeletePaths(ctx context.Context, token string, paths []string) error {
	_, err := doJSON(ctx, t.client, http.MethodPost,
		endpoint(t.base, nil, "secured", "filesystem", "delete"), token,
		map[string]any{"paths": paths})
	return err
}
