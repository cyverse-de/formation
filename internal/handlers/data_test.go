package handlers

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/cyverse-de/formation/internal/apierror"
	"github.com/cyverse-de/formation/internal/auth"
	"github.com/cyverse-de/formation/internal/authtest"
	"github.com/cyverse-de/formation/internal/datastore"
)

// fakeStore is an in-memory datastore.Store. Permissions default to allowing
// "alice" everywhere; set denyRead/denyWrite to restrict.
type fakeStore struct {
	files map[string][]byte
	dirs  map[string][]datastore.Entry
	meta  map[string][]datastore.AVU

	denyRead  []string
	denyWrite []string
	metaErr   error

	setMetaCalls []setMetaCall
	uploads      map[string][]byte
	createdDirs  []string
	deletedFiles []string
	deletedDirs  []deletedDir
}

type setMetaCall struct {
	path    string
	avus    []datastore.AVU
	replace bool
}

type deletedDir struct {
	path    string
	recurse bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		files:   map[string][]byte{},
		dirs:    map[string][]datastore.Entry{},
		meta:    map[string][]datastore.AVU{},
		uploads: map[string][]byte{},
	}
}

func (f *fakeStore) PathExists(p string) bool       { return f.FileExists(p) || f.CollectionExists(p) }
func (f *fakeStore) FileExists(p string) bool       { _, ok := f.files[p]; return ok }
func (f *fakeStore) CollectionExists(p string) bool { _, ok := f.dirs[p]; return ok }

func (f *fakeStore) UserCanRead(username, p string) bool {
	return username == "alice" && !slices.Contains(f.denyRead, p)
}

func (f *fakeStore) UserCanWrite(username, p string) bool {
	return username == "alice" && !slices.Contains(f.denyWrite, p)
}

func (f *fakeStore) ListCollection(p string) ([]datastore.Entry, error) {
	return f.dirs[p], nil
}

func (f *fakeStore) CountCollectionItems(p string) (int, error) {
	return len(f.dirs[p]), nil
}

type fakeFile struct{ *bytes.Reader }

func (fakeFile) Close() error { return nil }

func (f *fakeStore) OpenFile(p string) (io.ReadSeekCloser, error) {
	content, ok := f.files[p]
	if !ok {
		return nil, errors.New("no such file")
	}
	return fakeFile{bytes.NewReader(content)}, nil
}

func (f *fakeStore) UploadFile(p string, content io.Reader) error {
	data, err := io.ReadAll(content)
	if err != nil {
		return err
	}
	f.uploads[p] = data
	f.files[p] = data
	return nil
}

func (f *fakeStore) CreateDirectory(p string) error {
	f.createdDirs = append(f.createdDirs, p)
	f.dirs[p] = nil
	return nil
}

func (f *fakeStore) Metadata(p string) ([]datastore.AVU, error) {
	if f.metaErr != nil {
		return nil, f.metaErr
	}
	return f.meta[p], nil
}

func (f *fakeStore) SetMetadata(p string, avus []datastore.AVU, replace bool) error {
	f.setMetaCalls = append(f.setMetaCalls, setMetaCall{path: p, avus: avus, replace: replace})
	return nil
}

func (f *fakeStore) DeleteFile(p string) error {
	f.deletedFiles = append(f.deletedFiles, p)
	delete(f.files, p)
	return nil
}

func (f *fakeStore) DeleteDirectory(p string, recurse bool) error {
	f.deletedDirs = append(f.deletedDirs, deletedDir{path: p, recurse: recurse})
	delete(f.dirs, p)
	return nil
}

// dataEnv runs the /data routes behind the real RequireUser middleware.
type dataEnv struct {
	echo  *echo.Echo
	kc    *authtest.Keycloak
	store *fakeStore
}

func newDataEnv(t *testing.T) *dataEnv {
	t.Helper()

	kc := authtest.New(t, "de")
	verifier := auth.NewVerifier(kc.ServerURL(), kc.Realm, true)
	requireUser := auth.RequireUser(verifier)

	store := newFakeStore()
	data := NewData(store)

	e := echo.New()
	e.HTTPErrorHandler = apierror.HTTPErrorHandler
	e.GET("/data/*", data.Get, requireUser)
	e.PUT("/data/*", data.Put, requireUser)
	e.DELETE("/data/*", data.Delete, requireUser)

	return &dataEnv{echo: e, kc: kc, store: store}
}

func (env *dataEnv) request(t *testing.T, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set(echo.HeaderAuthorization,
		"Bearer "+env.kc.Token(t, map[string]any{"preferred_username": "alice"}))
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	env.echo.ServeHTTP(rec, req)
	return rec
}

func TestDataGetDirectory(t *testing.T) {
	env := newDataEnv(t)
	env.store.dirs["/iplant/home/alice"] = []datastore.Entry{
		{Name: "subdir", Type: "collection"},
		{Name: "file1.txt", Type: "data_object"},
	}

	rec := env.request(t, http.MethodGet, "/data/iplant/home/alice", "", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}

	body := decodeBody(t, rec)
	if body["path"] != "/iplant/home/alice" || body["type"] != "collection" {
		t.Errorf("body = %v", body)
	}
	contents, _ := body["contents"].([]any)
	if len(contents) != 2 {
		t.Fatalf("contents = %v", body["contents"])
	}
	first, _ := contents[0].(map[string]any)
	if first["name"] != "subdir" || first["type"] != "collection" {
		t.Errorf("first entry = %v", first)
	}
}

func TestDataGetErrors(t *testing.T) {
	env := newDataEnv(t)
	env.store.files["/iplant/secret.txt"] = []byte("hidden")
	env.store.denyRead = []string{"/iplant/secret.txt"}

	t.Run("missing path is 404", func(t *testing.T) {
		rec := env.request(t, http.MethodGet, "/data/iplant/nope", "", nil)
		if rec.Code != 404 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		if body := decodeBody(t, rec); body["detail"] != "Path '/iplant/nope' not found" {
			t.Errorf("detail = %v", body["detail"])
		}
	})

	t.Run("unreadable path is 403", func(t *testing.T) {
		rec := env.request(t, http.MethodGet, "/data/iplant/secret.txt", "", nil)
		if rec.Code != 403 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		if body := decodeBody(t, rec); body["detail"] != "Access denied" {
			t.Errorf("detail = %v", body["detail"])
		}
	})
}

func TestDataGetFile(t *testing.T) {
	content := "0123456789"
	tests := []struct {
		name   string
		target string
		want   string
	}{
		{"full contents", "/data/iplant/file.txt", content},
		{"offset", "/data/iplant/file.txt?offset=4", "456789"},
		{"limit", "/data/iplant/file.txt?limit=3", "012"},
		{"offset and limit", "/data/iplant/file.txt?offset=2&limit=4", "2345"},
		{"offset past end", "/data/iplant/file.txt?offset=100", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newDataEnv(t)
			env.store.files["/iplant/file.txt"] = []byte(content)

			rec := env.request(t, http.MethodGet, tt.target, "", nil)
			if rec.Code != 200 {
				t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
			}
			if rec.Body.String() != tt.want {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.want)
			}
			if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
				t.Errorf("Content-Type = %q, want text/plain", got)
			}
		})
	}

	t.Run("unknown extension is octet-stream", func(t *testing.T) {
		env := newDataEnv(t)
		env.store.files["/iplant/file.xyzzy"] = []byte("data")
		rec := env.request(t, http.MethodGet, "/data/iplant/file.xyzzy", "", nil)
		if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
			t.Errorf("Content-Type = %q", got)
		}
	})
}

// rawHeader returns the values stored under the exact header key: metadata
// headers deliberately bypass Go's canonicalization to preserve iRODS
// attribute case, so canonical map lookups would miss them.
func rawHeader(rec *httptest.ResponseRecorder, key string) []string {
	for name, values := range rec.Header() {
		if name == key {
			return values
		}
	}
	return nil
}

func TestDataGetMetadataHeaders(t *testing.T) {
	env := newDataEnv(t)
	env.store.files["/iplant/file.txt"] = []byte("x")
	env.store.meta["/iplant/file.txt"] = []datastore.AVU{
		{Attribute: "ipc_UUID", Value: "abc-123"},
		{Attribute: "weight", Value: "12", Units: "kg"},
	}

	t.Run("disabled by default", func(t *testing.T) {
		rec := env.request(t, http.MethodGet, "/data/iplant/file.txt", "", nil)
		if got := rawHeader(rec, "X-Datastore-ipc_UUID"); got != nil {
			t.Error("metadata headers should be absent without include_metadata")
		}
	})

	t.Run("default delimiter", func(t *testing.T) {
		rec := env.request(t, http.MethodGet, "/data/iplant/file.txt?include_metadata=true", "", nil)
		// Attribute case from iRODS is preserved in header names.
		if got := rawHeader(rec, "X-Datastore-ipc_UUID"); len(got) != 1 || got[0] != "abc-123" {
			t.Errorf("X-Datastore-ipc_UUID = %v", got)
		}
		if got := rawHeader(rec, "X-Datastore-weight"); len(got) != 1 || got[0] != "12,kg" {
			t.Errorf("X-Datastore-weight = %v", got)
		}
	})

	t.Run("custom delimiter", func(t *testing.T) {
		rec := env.request(t, http.MethodGet, "/data/iplant/file.txt?include_metadata=true&avu_delimiter=%3B", "", nil)
		if got := rawHeader(rec, "X-Datastore-weight"); len(got) != 1 || got[0] != "12;kg" {
			t.Errorf("X-Datastore-weight = %v", got)
		}
	})

	t.Run("metadata errors are swallowed", func(t *testing.T) {
		env.store.metaErr = errors.New("boom")
		defer func() { env.store.metaErr = nil }()
		rec := env.request(t, http.MethodGet, "/data/iplant/file.txt?include_metadata=true", "", nil)
		if rec.Code != 200 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		if got := rawHeader(rec, "X-Datastore-ipc_UUID"); got != nil {
			t.Error("headers should be empty when metadata lookup fails")
		}
	})
}

func TestDataPutCreateFile(t *testing.T) {
	env := newDataEnv(t)
	env.store.dirs["/iplant/home/alice"] = nil

	rec := env.request(t, http.MethodPut, "/data/iplant/home/alice/new.txt", "hello world",
		map[string]string{"X-Datastore-Author": "alice", "X-Datastore-Weight": "12,kg"})
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}

	body := decodeBody(t, rec)
	if body["path"] != "/iplant/home/alice/new.txt" || body["type"] != "data_object" || body["created"] != true {
		t.Errorf("body = %v", body)
	}
	if got := string(env.store.uploads["/iplant/home/alice/new.txt"]); got != "hello world" {
		t.Errorf("uploaded content = %q", got)
	}

	if len(env.store.setMetaCalls) != 1 {
		t.Fatalf("setMetadata calls = %d, want 1", len(env.store.setMetaCalls))
	}
	// Attribute names are lowercased like the Python version; units split on
	// the delimiter; sorted by attribute.
	want := []datastore.AVU{{Attribute: "author", Value: "alice"}, {Attribute: "weight", Value: "12", Units: "kg"}}
	if got := env.store.setMetaCalls[0].avus; !slices.Equal(got, want) {
		t.Errorf("avus = %v, want %v", got, want)
	}
	if env.store.setMetaCalls[0].replace {
		t.Error("replace should default to false")
	}
}

func TestDataPutUpdateFile(t *testing.T) {
	env := newDataEnv(t)
	env.store.files["/iplant/file.txt"] = []byte("old")

	rec := env.request(t, http.MethodPut, "/data/iplant/file.txt", "new contents", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	if body["created"] != false || body["type"] != "data_object" {
		t.Errorf("body = %v", body)
	}
	if got := string(env.store.uploads["/iplant/file.txt"]); got != "new contents" {
		t.Errorf("uploaded content = %q", got)
	}
	if len(env.store.setMetaCalls) != 0 {
		t.Error("setMetadata should not be called without metadata headers")
	}
}

func TestDataPutMetadataOnly(t *testing.T) {
	t.Run("on a file with replace", func(t *testing.T) {
		env := newDataEnv(t)
		env.store.files["/iplant/file.txt"] = []byte("x")

		rec := env.request(t, http.MethodPut, "/data/iplant/file.txt?replace_metadata=true", "",
			map[string]string{"X-Datastore-Author": "bob"})
		if rec.Code != 200 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		body := decodeBody(t, rec)
		if body["type"] != "data_object" || body["created"] != false {
			t.Errorf("body = %v", body)
		}
		if len(env.store.setMetaCalls) != 1 || !env.store.setMetaCalls[0].replace {
			t.Errorf("setMetadata calls = %+v, want one replace call", env.store.setMetaCalls)
		}
	})

	t.Run("on a collection", func(t *testing.T) {
		env := newDataEnv(t)
		env.store.dirs["/iplant/dir"] = nil

		rec := env.request(t, http.MethodPut, "/data/iplant/dir", "",
			map[string]string{"X-Datastore-Project": "proj1"})
		if rec.Code != 200 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		body := decodeBody(t, rec)
		if body["type"] != "collection" || body["created"] != false {
			t.Errorf("body = %v", body)
		}
		if len(env.store.setMetaCalls) != 1 || env.store.setMetaCalls[0].path != "/iplant/dir" {
			t.Errorf("setMetadata calls = %+v", env.store.setMetaCalls)
		}
	})
}

func TestDataPutCreateDirectory(t *testing.T) {
	env := newDataEnv(t)
	env.store.dirs["/iplant/home/alice"] = nil

	rec := env.request(t, http.MethodPut, "/data/iplant/home/alice/newdir?resource_type=directory", "", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	if body["type"] != "collection" || body["created"] != true {
		t.Errorf("body = %v", body)
	}
	if !slices.Contains(env.store.createdDirs, "/iplant/home/alice/newdir") {
		t.Errorf("createdDirs = %v", env.store.createdDirs)
	}
}

func TestDataPutErrors(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(store *fakeStore)
		target     string
		body       string
		wantStatus int
		wantDetail string
	}{
		{
			name:       "upload to a directory",
			setup:      func(s *fakeStore) { s.dirs["/iplant/dir"] = nil },
			target:     "/data/iplant/dir",
			body:       "content",
			wantStatus: 400,
			wantDetail: "Cannot upload file - path is a directory",
		},
		{
			name:       "missing parent",
			setup:      func(s *fakeStore) {},
			target:     "/data/iplant/nope/new.txt",
			body:       "content",
			wantStatus: 404,
			wantDetail: "Parent directory '/iplant/nope' not found",
		},
		{
			name: "unwritable parent",
			setup: func(s *fakeStore) {
				s.dirs["/iplant/readonly"] = nil
				s.denyWrite = []string{"/iplant/readonly"}
			},
			target:     "/data/iplant/readonly/new.txt",
			body:       "content",
			wantStatus: 403,
			wantDetail: "Access denied",
		},
		{
			name: "unwritable existing path",
			setup: func(s *fakeStore) {
				s.files["/iplant/locked.txt"] = []byte("x")
				s.denyWrite = []string{"/iplant/locked.txt"}
			},
			target:     "/data/iplant/locked.txt",
			body:       "content",
			wantStatus: 403,
			wantDetail: "Access denied",
		},
		{
			name:       "ambiguous request",
			setup:      func(s *fakeStore) { s.dirs["/iplant"] = nil },
			target:     "/data/iplant/new-thing",
			body:       "",
			wantStatus: 400,
			wantDetail: "Cannot determine operation: provide file content or type=directory parameter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newDataEnv(t)
			tt.setup(env.store)

			rec := env.request(t, http.MethodPut, tt.target, tt.body, nil)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
			}
			if body := decodeBody(t, rec); body["detail"] != tt.wantDetail {
				t.Errorf("detail = %v, want %q", body["detail"], tt.wantDetail)
			}
		})
	}
}

func TestDataDeleteFile(t *testing.T) {
	t.Run("real delete", func(t *testing.T) {
		env := newDataEnv(t)
		env.store.files["/iplant/file.txt"] = []byte("x")

		rec := env.request(t, http.MethodDelete, "/data/iplant/file.txt", "", nil)
		if rec.Code != 200 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		body := decodeBody(t, rec)
		want := map[string]any{
			"path": "/iplant/file.txt", "type": "data_object",
			"would_delete": true, "deleted": true, "dry_run": false,
		}
		for key, value := range want {
			if body[key] != value {
				t.Errorf("body[%q] = %v, want %v", key, body[key], value)
			}
		}
		if !slices.Contains(env.store.deletedFiles, "/iplant/file.txt") {
			t.Errorf("deletedFiles = %v", env.store.deletedFiles)
		}
	})

	t.Run("dry run does not delete", func(t *testing.T) {
		env := newDataEnv(t)
		env.store.files["/iplant/file.txt"] = []byte("x")

		rec := env.request(t, http.MethodDelete, "/data/iplant/file.txt?dry_run=true", "", nil)
		body := decodeBody(t, rec)
		if body["deleted"] != false || body["dry_run"] != true || body["would_delete"] != true {
			t.Errorf("body = %v", body)
		}
		if len(env.store.deletedFiles) != 0 {
			t.Errorf("deletedFiles = %v, want none", env.store.deletedFiles)
		}
	})
}

func TestDataDeleteDirectory(t *testing.T) {
	nonEmpty := []datastore.Entry{{Name: "child.txt", Type: "data_object"}}

	t.Run("empty dir without recurse", func(t *testing.T) {
		env := newDataEnv(t)
		env.store.dirs["/iplant/empty"] = nil

		rec := env.request(t, http.MethodDelete, "/data/iplant/empty", "", nil)
		if rec.Code != 200 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		body := decodeBody(t, rec)
		if body["type"] != "collection" || body["deleted"] != true {
			t.Errorf("body = %v", body)
		}
		if _, ok := body["item_count"]; ok {
			t.Error("item_count should be absent without recurse")
		}
	})

	t.Run("non-empty dir without recurse is 400", func(t *testing.T) {
		env := newDataEnv(t)
		env.store.dirs["/iplant/full"] = nonEmpty

		rec := env.request(t, http.MethodDelete, "/data/iplant/full", "", nil)
		if rec.Code != 400 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		if body := decodeBody(t, rec); body["detail"] != "Directory not empty. Use recurse=true to delete non-empty directories." {
			t.Errorf("detail = %v", body["detail"])
		}
		if len(env.store.deletedDirs) != 0 {
			t.Errorf("deletedDirs = %v, want none", env.store.deletedDirs)
		}
	})

	t.Run("dry run on non-empty dir without recurse matches real outcome", func(t *testing.T) {
		env := newDataEnv(t)
		env.store.dirs["/iplant/full"] = nonEmpty

		rec := env.request(t, http.MethodDelete, "/data/iplant/full?dry_run=true", "", nil)
		if rec.Code != 400 {
			t.Fatalf("status = %d, body %s (dry run should report the same 400)", rec.Code, rec.Body)
		}
	})

	t.Run("recurse reports item_count", func(t *testing.T) {
		env := newDataEnv(t)
		env.store.dirs["/iplant/full"] = nonEmpty

		rec := env.request(t, http.MethodDelete, "/data/iplant/full?recurse=true", "", nil)
		if rec.Code != 200 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		body := decodeBody(t, rec)
		if body["item_count"] != float64(1) || body["deleted"] != true {
			t.Errorf("body = %v", body)
		}
		if len(env.store.deletedDirs) != 1 || !env.store.deletedDirs[0].recurse {
			t.Errorf("deletedDirs = %+v", env.store.deletedDirs)
		}
	})

	t.Run("recurse dry run keeps item_count but does not delete", func(t *testing.T) {
		env := newDataEnv(t)
		env.store.dirs["/iplant/full"] = nonEmpty

		rec := env.request(t, http.MethodDelete, "/data/iplant/full?recurse=true&dry_run=true", "", nil)
		body := decodeBody(t, rec)
		if body["item_count"] != float64(1) || body["deleted"] != false || body["dry_run"] != true {
			t.Errorf("body = %v", body)
		}
		if len(env.store.deletedDirs) != 0 {
			t.Errorf("deletedDirs = %v, want none", env.store.deletedDirs)
		}
	})
}

func TestDataDeleteErrors(t *testing.T) {
	env := newDataEnv(t)
	env.store.files["/iplant/locked.txt"] = []byte("x")
	env.store.denyWrite = []string{"/iplant/locked.txt"}

	t.Run("missing path is 404", func(t *testing.T) {
		rec := env.request(t, http.MethodDelete, "/data/iplant/nope", "", nil)
		if rec.Code != 404 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
	})

	t.Run("unwritable path is 403", func(t *testing.T) {
		rec := env.request(t, http.MethodDelete, "/data/iplant/locked.txt", "", nil)
		if rec.Code != 403 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
	})

	t.Run("bad boolean param is 400", func(t *testing.T) {
		rec := env.request(t, http.MethodDelete, "/data/iplant/locked.txt?recurse=maybe", "", nil)
		if rec.Code != 400 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		if body := decodeBody(t, rec); body["detail"] != "Invalid boolean value for recurse" {
			t.Errorf("detail = %v", body["detail"])
		}
	})
}
