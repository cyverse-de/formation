package terraintest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	gopath "path"
	"strconv"
	"strings"
	"unicode/utf8"
)

// DataAVU mirrors terrain's iRODS AVU payload shape.
type DataAVU struct {
	Attr  string `json:"attr"`
	Value string `json:"value"`
	Unit  string `json:"unit"`
}

// MetadataSet records one full-listing metadata POST.
type MetadataSet struct {
	Path string
	AVUs []DataAVU
}

// DataEntry is one member of a fake directory.
type DataEntry struct {
	Name string
	Dir  bool
}

// Data is an in-memory fake of terrain's filesystem and fileio endpoints,
// mimicking the data-info behaviors formation depends on (as observed against
// QA): ERR_DOES_NOT_EXIST as a 500, stat omitting unreadable paths from a 200
// response, paged listings, random-access chunk reads, upload/overwrite,
// directory creation, full-listing metadata set, and recursive (trash) deletion.
type Data struct {
	Files map[string][]byte
	Dirs  map[string][]DataEntry
	// Perms maps a path to the caller's effective permission ("read",
	// "write", "own", or "none" for unreadable); missing paths default to "own".
	Perms map[string]string
	Meta  map[string][]DataAVU
	// MetaErr makes metadata lookups fail, like a metadata-service outage.
	MetaErr bool

	CreatedDirs []string
	Deleted     []string
	MetaSets    []MetadataSet
	MetaAdds    []MetadataSet
}

// NewData returns an empty fake data store.
func NewData() *Data {
	return &Data{
		Files: map[string][]byte{},
		Dirs:  map[string][]DataEntry{},
		Perms: map[string]string{},
		Meta:  map[string][]DataAVU{},
	}
}

func (d *Data) exists(p string) bool {
	if _, ok := d.Files[p]; ok {
		return true
	}
	_, ok := d.Dirs[p]
	return ok
}

func (d *Data) isDir(p string) bool {
	_, ok := d.Dirs[p]
	return ok
}

func (d *Data) permission(p string) string {
	if perm, ok := d.Perms[p]; ok {
		return perm
	}
	return "own"
}

func (d *Data) writable(p string) bool {
	perm := d.permission(p)
	return perm == "write" || perm == "own"
}

// dataID returns the data-id served for a path; the metadata routes map it back.
func dataID(p string) string {
	return url.PathEscape(p)
}

func errorBody(code string) map[string]any {
	return map[string]any{"error_code": code}
}

// statRecord builds the stat entry for an existing path.
func (d *Data) statRecord(p string) map[string]any {
	record := map[string]any{
		"id":         dataID(p),
		"path":       p,
		"permission": d.permission(p),
		"type":       "file",
	}
	if entries, ok := d.Dirs[p]; ok {
		record["type"] = "dir"
		files, dirs := 0, 0
		for _, entry := range entries {
			if entry.Dir {
				dirs++
			} else {
				files++
			}
		}
		record["file-count"] = files
		record["dir-count"] = dirs
	}
	return record
}

// check returns the error status/body for a missing or unreadable path.
// Like real terrain (observed in QA), ERR_DOES_NOT_EXIST arrives as a 500,
// and iRODS hides unreadable paths so they also read as nonexistent.
func (d *Data) check(p string) (int, map[string]any) {
	if !d.exists(p) || d.permission(p) == "none" {
		return http.StatusInternalServerError, errorBody("ERR_DOES_NOT_EXIST")
	}
	return 0, nil
}

// Respond serves terrain's data endpoints; use it as (or within) a Server
// respond function.
func (d *Data) Respond(r *http.Request) (int, any) {
	p := r.URL.Path
	switch {
	case r.Method == http.MethodPost && p == "/secured/filesystem/stat":
		return d.stat(r)
	case r.Method == http.MethodGet && p == "/secured/filesystem/paged-directory":
		return d.listDirectory(r)
	case r.Method == http.MethodPost && p == "/secured/filesystem/read-chunk":
		return d.readChunk(r)
	case r.Method == http.MethodPost && p == "/secured/fileio/upload":
		return d.upload(r)
	case r.Method == http.MethodPost && p == "/secured/fileio/overwrite":
		return d.overwrite(r)
	case r.Method == http.MethodPost && p == "/secured/filesystem/directory/create":
		return d.createDirectory(r)
	case r.Method == http.MethodPost && p == "/secured/filesystem/delete":
		return d.delete(r)
	case strings.HasPrefix(p, "/secured/filesystem/") && strings.HasSuffix(p, "/metadata"):
		return d.metadata(r)
	case r.Method == http.MethodPost && strings.HasPrefix(p, "/secured/filesystem/") && strings.HasSuffix(p, "/metadata/add"):
		return d.metadataAdd(r)
	default:
		return http.StatusInternalServerError, nil
	}
}

// metadataPath resolves the data-id segment of a metadata route back to a path.
func (d *Data) metadataPath(r *http.Request, suffix string) (string, bool) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/secured/filesystem/"), suffix)
	p, err := url.PathUnescape(id)
	if err != nil || !d.exists(p) {
		return "", false
	}
	return p, true
}

func (d *Data) metadataAdd(r *http.Request) (int, any) {
	p, ok := d.metadataPath(r, "/metadata/add")
	if !ok {
		return http.StatusNotFound, errorBody("ERR_NOT_FOUND")
	}
	if !d.writable(p) {
		return http.StatusInternalServerError, errorBody("ERR_NOT_WRITEABLE")
	}

	var body struct {
		IRODSAVUs []DataAVU `json:"irods-avus"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	// Exact duplicates are silently skipped, like data-info's add.
	existing := make(map[DataAVU]bool, len(d.Meta[p]))
	for _, avu := range d.Meta[p] {
		existing[avu] = true
	}
	for _, avu := range body.IRODSAVUs {
		if !existing[avu] {
			d.Meta[p] = append(d.Meta[p], avu)
		}
	}
	d.MetaAdds = append(d.MetaAdds, MetadataSet{Path: p, AVUs: body.IRODSAVUs})
	return http.StatusOK, map[string]any{"path": p, "user": "fake"}
}

func (d *Data) stat(r *http.Request) (int, any) {
	var body struct {
		Paths []string `json:"paths"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	paths := make(map[string]any, len(body.Paths))
	for _, p := range body.Paths {
		if !d.exists(p) {
			return http.StatusInternalServerError, errorBody("ERR_DOES_NOT_EXIST")
		}
		// Stat is the one endpoint that can see unreadable paths: data-info
		// validates existence, then silently omits entries the caller cannot
		// read, so the response is a 200 with the path missing.
		if d.permission(p) == "none" {
			continue
		}
		paths[p] = d.statRecord(p)
	}
	return http.StatusOK, map[string]any{"paths": paths}
}

func (d *Data) listDirectory(r *http.Request) (int, any) {
	query := r.URL.Query()
	p := query.Get("path")
	if status, errBody := d.check(p); status != 0 {
		return status, errBody
	}

	entries := d.Dirs[p]
	total := len(entries)
	// Honor limit/offset like terrain's paged listing so the client's paging
	// loop is exercised; limit <= 0 returns everything.
	offset, _ := strconv.Atoi(query.Get("offset"))
	limit, _ := strconv.Atoi(query.Get("limit"))
	offset = min(max(offset, 0), total)
	end := total
	if limit > 0 {
		end = min(offset+limit, total)
	}

	var folders, files []map[string]any
	for _, entry := range entries[offset:end] {
		item := map[string]any{"label": entry.Name, "path": gopath.Join(p, entry.Name)}
		if entry.Dir {
			folders = append(folders, item)
		} else {
			files = append(files, item)
		}
	}
	return http.StatusOK, map[string]any{
		"path":    p,
		"total":   total,
		"folders": folders,
		"files":   files,
	}
}

// readChunk serves terrain's random-access read-chunk endpoint, returning the
// data-info chunk record (the requested byte window plus the total file size).
// Edge behaviors mimic data-info as observed against QA: a negative position
// is an unchecked exception, a position past EOF reads as empty, and the bytes
// of a rune split across a window edge are silently dropped.
func (d *Data) readChunk(r *http.Request) (int, any) {
	var body struct {
		Path      string `json:"path"`
		Position  int    `json:"position"`
		ChunkSize int    `json:"chunk-size"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Position < 0 || body.ChunkSize <= 0 {
		return http.StatusInternalServerError, errorBody("ERR_UNCHECKED_EXCEPTION")
	}
	if status, errBody := d.check(body.Path); status != 0 {
		return status, errBody
	}
	content, ok := d.Files[body.Path]
	if !ok {
		return http.StatusBadRequest, errorBody("ERR_NOT_A_FILE")
	}

	start := min(body.Position, len(content))
	end := min(start+body.ChunkSize, len(content))
	return http.StatusOK, map[string]any{
		"path":       body.Path,
		"user":       "fake",
		"start":      strconv.Itoa(start),
		"chunk-size": strconv.Itoa(body.ChunkSize),
		"file-size":  strconv.Itoa(len(content)),
		"chunk":      string(trimSplitRunes(content[start:end])),
	}
}

// trimSplitRunes mimics data-info's string decoding at the window edges:
// leading continuation bytes and a trailing incomplete rune are dropped.
// Interior invalid bytes are left alone — the JSON encoding replaces them
// with U+FFFD, like data-info's decoder does.
func trimSplitRunes(window []byte) []byte {
	for len(window) > 0 && !utf8.RuneStart(window[0]) {
		window = window[1:]
	}
	for range utf8.UTFMax - 1 {
		if len(window) == 0 {
			break
		}
		if r, _ := utf8.DecodeLastRune(window); r != utf8.RuneError {
			break
		}
		window = window[:len(window)-1]
	}
	return window
}

// filePart reads the multipart "file" part's contents and filename.
func filePart(r *http.Request) (content []byte, filename string, ok bool) {
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, "", false
	}
	for {
		part, err := reader.NextPart()
		if err != nil {
			return nil, "", false
		}
		if part.FormName() == "file" {
			content, err := io.ReadAll(part)
			return content, part.FileName(), err == nil
		}
	}
}

func (d *Data) upload(r *http.Request) (int, any) {
	dest := r.URL.Query().Get("dest")
	if status, errBody := d.check(dest); status != 0 {
		return status, errBody
	}
	if !d.writable(dest) {
		return http.StatusInternalServerError, errorBody("ERR_NOT_WRITEABLE")
	}

	content, filename, ok := filePart(r)
	if !ok {
		return http.StatusBadRequest, errorBody("ERR_BAD_OR_MISSING_FIELD")
	}
	target := gopath.Join(dest, filename)
	if d.exists(target) {
		return http.StatusConflict, errorBody("ERR_EXISTS")
	}

	d.Files[target] = content
	d.Dirs[dest] = append(d.Dirs[dest], DataEntry{Name: filename})
	return http.StatusOK, map[string]any{"file": map[string]any{"id": dataID(target), "path": target}}
}

func (d *Data) overwrite(r *http.Request) (int, any) {
	dest := r.URL.Query().Get("dest")
	if status, errBody := d.check(dest); status != 0 {
		return status, errBody
	}
	if !d.writable(dest) {
		return http.StatusInternalServerError, errorBody("ERR_NOT_WRITEABLE")
	}
	if d.isDir(dest) {
		return http.StatusBadRequest, errorBody("ERR_NOT_A_FILE")
	}

	content, _, ok := filePart(r)
	if !ok {
		return http.StatusBadRequest, errorBody("ERR_BAD_OR_MISSING_FIELD")
	}
	d.Files[dest] = content
	return http.StatusOK, map[string]any{"file": map[string]any{"id": dataID(dest), "path": dest}}
}

func (d *Data) createDirectory(r *http.Request) (int, any) {
	var body struct {
		Path string `json:"path"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	parent := gopath.Dir(body.Path)
	if status, errBody := d.check(parent); status != 0 {
		return status, errBody
	}
	if !d.writable(parent) {
		return http.StatusInternalServerError, errorBody("ERR_NOT_WRITEABLE")
	}
	if d.exists(body.Path) {
		return http.StatusConflict, errorBody("ERR_EXISTS")
	}

	d.Dirs[body.Path] = nil
	d.CreatedDirs = append(d.CreatedDirs, body.Path)
	d.Dirs[parent] = append(d.Dirs[parent], DataEntry{Name: gopath.Base(body.Path), Dir: true})
	return http.StatusOK, map[string]any{"path": body.Path}
}

func (d *Data) delete(r *http.Request) (int, any) {
	var body struct {
		Paths []string `json:"paths"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	for _, p := range body.Paths {
		if status, errBody := d.check(p); status != 0 {
			return status, errBody
		}
		if !d.writable(p) {
			return http.StatusInternalServerError, errorBody("ERR_NOT_WRITEABLE")
		}
		delete(d.Files, p)
		delete(d.Dirs, p)
		d.Deleted = append(d.Deleted, p)
	}
	return http.StatusOK, map[string]any{"paths": body.Paths}
}

func (d *Data) metadata(r *http.Request) (int, any) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/secured/filesystem/"), "/metadata")
	p, err := url.PathUnescape(id)
	if err != nil || !d.exists(p) {
		return http.StatusNotFound, errorBody("ERR_NOT_FOUND")
	}

	switch r.Method {
	case http.MethodGet:
		if d.MetaErr {
			return http.StatusInternalServerError, errorBody("ERR_UNCHECKED_EXCEPTION")
		}
		avus := d.Meta[p]
		if avus == nil {
			avus = []DataAVU{}
		}
		return http.StatusOK, map[string]any{"path": p, "irods-avus": avus, "avus": []any{}}

	case http.MethodPost:
		if !d.writable(p) {
			return http.StatusInternalServerError, errorBody("ERR_NOT_WRITEABLE")
		}
		var body struct {
			IRODSAVUs []DataAVU `json:"irods-avus"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		d.Meta[p] = body.IRODSAVUs
		d.MetaSets = append(d.MetaSets, MetadataSet{Path: p, AVUs: body.IRODSAVUs})
		return http.StatusOK, map[string]any{"path": p, "user": "fake"}

	default:
		return http.StatusMethodNotAllowed, nil
	}
}
