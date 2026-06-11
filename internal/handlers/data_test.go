package handlers

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/cyverse-de/formation/internal/apierror"
	"github.com/cyverse-de/formation/internal/auth"
	"github.com/cyverse-de/formation/internal/authtest"
	"github.com/cyverse-de/formation/internal/clients"
	"github.com/cyverse-de/formation/internal/terraintest"
)

// dataEnv runs the /data routes behind the real RequireUser middleware with a
// fake terrain serving the data endpoints.
type dataEnv struct {
	echo *echo.Echo
	kc   *authtest.Keycloak
	data *terraintest.Data
}

func newDataEnv(t *testing.T) *dataEnv {
	t.Helper()

	fakeData := terraintest.NewData()
	terrain := terraintest.New(t, fakeData.Respond)
	terrainClient, err := clients.NewTerrain(terrain.URL())
	if err != nil {
		t.Fatal(err)
	}

	kc := authtest.New(t, "de")
	verifier := auth.NewVerifier(kc.ServerURL(), kc.Realm, true)
	requireUser := auth.RequireUser(verifier)

	data := NewData(terrainClient)

	e := echo.New()
	e.HTTPErrorHandler = apierror.HTTPErrorHandler
	e.GET("/data/*", data.Get, requireUser)
	e.PUT("/data/*", data.Put, requireUser)
	e.DELETE("/data/*", data.Delete, requireUser)

	return &dataEnv{echo: e, kc: kc, data: fakeData}
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
	env.data.Dirs["/iplant/home/alice"] = []terraintest.DataEntry{
		{Name: "subdir", Dir: true},
		{Name: "file1.txt"},
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
	second, _ := contents[1].(map[string]any)
	if second["name"] != "file1.txt" || second["type"] != "data_object" {
		t.Errorf("second entry = %v", second)
	}
}

func TestDataGetErrors(t *testing.T) {
	env := newDataEnv(t)
	env.data.Files["/iplant/secret.txt"] = []byte("hidden")
	env.data.Perms["/iplant/secret.txt"] = "none"

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
			env.data.Files["/iplant/file.txt"] = []byte(content)

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
		env.data.Files["/iplant/file.xyzzy"] = []byte("data")
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
	env.data.Files["/iplant/file.txt"] = []byte("x")
	env.data.Meta["/iplant/file.txt"] = []terraintest.DataAVU{
		{Attr: "Sample_Type", Value: "abc-123"},
		{Attr: "weight", Value: "12", Unit: "kg"},
	}

	t.Run("disabled by default", func(t *testing.T) {
		rec := env.request(t, http.MethodGet, "/data/iplant/file.txt", "", nil)
		if got := rawHeader(rec, "X-Datastore-Sample_Type"); got != nil {
			t.Error("metadata headers should be absent without include_metadata")
		}
	})

	t.Run("default delimiter", func(t *testing.T) {
		rec := env.request(t, http.MethodGet, "/data/iplant/file.txt?include_metadata=true", "", nil)
		// Attribute case from iRODS is preserved in header names.
		if got := rawHeader(rec, "X-Datastore-Sample_Type"); len(got) != 1 || got[0] != "abc-123" {
			t.Errorf("X-Datastore-Sample_Type = %v", got)
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
		env.data.MetaErr = true
		defer func() { env.data.MetaErr = false }()
		rec := env.request(t, http.MethodGet, "/data/iplant/file.txt?include_metadata=true", "", nil)
		if rec.Code != 200 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		if got := rawHeader(rec, "X-Datastore-Sample_Type"); got != nil {
			t.Error("headers should be empty when metadata lookup fails")
		}
	})
}

func TestDataPutCreateFile(t *testing.T) {
	env := newDataEnv(t)
	env.data.Dirs["/iplant/home/alice"] = nil

	rec := env.request(t, http.MethodPut, "/data/iplant/home/alice/new.txt", "hello world",
		map[string]string{"X-Datastore-Author": "alice", "X-Datastore-Weight": "12,kg"})
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}

	body := decodeBody(t, rec)
	if body["path"] != "/iplant/home/alice/new.txt" || body["type"] != "data_object" || body["created"] != true {
		t.Errorf("body = %v", body)
	}
	if got := string(env.data.Uploads["/iplant/home/alice/new.txt"]); got != "hello world" {
		t.Errorf("uploaded content = %q", got)
	}

	if len(env.data.MetaSets) != 1 {
		t.Fatalf("metadata sets = %d, want 1", len(env.data.MetaSets))
	}
	// Attribute names are lowercased like the Python version; units split on
	// the delimiter; sorted by attribute.
	want := []terraintest.DataAVU{{Attr: "author", Value: "alice"}, {Attr: "weight", Value: "12", Unit: "kg"}}
	if got := env.data.MetaSets[0].AVUs; !slices.Equal(got, want) {
		t.Errorf("avus = %v, want %v", got, want)
	}
}

func TestDataPutUpdateFile(t *testing.T) {
	env := newDataEnv(t)
	env.data.Files["/iplant/file.txt"] = []byte("old")

	rec := env.request(t, http.MethodPut, "/data/iplant/file.txt", "new contents", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	if body["created"] != false || body["type"] != "data_object" {
		t.Errorf("body = %v", body)
	}
	if got := string(env.data.Uploads["/iplant/file.txt"]); got != "new contents" {
		t.Errorf("uploaded content = %q", got)
	}
	if len(env.data.MetaSets) != 0 {
		t.Error("metadata should not be set without metadata headers")
	}
}

func TestDataPutMetadataOnly(t *testing.T) {
	t.Run("replace clears the attributes being set", func(t *testing.T) {
		env := newDataEnv(t)
		env.data.Files["/iplant/file.txt"] = []byte("x")
		env.data.Meta["/iplant/file.txt"] = []terraintest.DataAVU{
			{Attr: "author", Value: "old-author"},
			{Attr: "project", Value: "proj0"},
		}

		rec := env.request(t, http.MethodPut, "/data/iplant/file.txt?replace_metadata=true", "",
			map[string]string{"X-Datastore-Author": "bob"})
		if rec.Code != 200 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		body := decodeBody(t, rec)
		if body["type"] != "data_object" || body["created"] != false {
			t.Errorf("body = %v", body)
		}
		// author's existing AVU is replaced; project survives untouched.
		want := []terraintest.DataAVU{{Attr: "project", Value: "proj0"}, {Attr: "author", Value: "bob"}}
		if len(env.data.MetaSets) != 1 || !slices.Equal(env.data.MetaSets[0].AVUs, want) {
			t.Errorf("metadata sets = %+v, want %v", env.data.MetaSets, want)
		}
	})

	t.Run("without replace adds alongside existing values", func(t *testing.T) {
		env := newDataEnv(t)
		env.data.Files["/iplant/file.txt"] = []byte("x")
		env.data.Meta["/iplant/file.txt"] = []terraintest.DataAVU{{Attr: "author", Value: "old-author"}}

		rec := env.request(t, http.MethodPut, "/data/iplant/file.txt", "",
			map[string]string{"X-Datastore-Author": "bob"})
		if rec.Code != 200 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		want := []terraintest.DataAVU{{Attr: "author", Value: "old-author"}, {Attr: "author", Value: "bob"}}
		if len(env.data.MetaSets) != 1 || !slices.Equal(env.data.MetaSets[0].AVUs, want) {
			t.Errorf("metadata sets = %+v, want %v", env.data.MetaSets, want)
		}
	})

	t.Run("on a collection", func(t *testing.T) {
		env := newDataEnv(t)
		env.data.Dirs["/iplant/dir"] = nil

		rec := env.request(t, http.MethodPut, "/data/iplant/dir", "",
			map[string]string{"X-Datastore-Project": "proj1"})
		if rec.Code != 200 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		body := decodeBody(t, rec)
		if body["type"] != "collection" || body["created"] != false {
			t.Errorf("body = %v", body)
		}
		if len(env.data.MetaSets) != 1 || env.data.MetaSets[0].Path != "/iplant/dir" {
			t.Errorf("metadata sets = %+v", env.data.MetaSets)
		}
	})
}

func TestDataPutCreateDirectory(t *testing.T) {
	env := newDataEnv(t)
	env.data.Dirs["/iplant/home/alice"] = nil

	rec := env.request(t, http.MethodPut, "/data/iplant/home/alice/newdir?resource_type=directory", "", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	if body["type"] != "collection" || body["created"] != true {
		t.Errorf("body = %v", body)
	}
	if !slices.Contains(env.data.CreatedDirs, "/iplant/home/alice/newdir") {
		t.Errorf("createdDirs = %v", env.data.CreatedDirs)
	}
}

func TestDataPutErrors(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(data *terraintest.Data)
		target     string
		body       string
		wantStatus int
		wantDetail string
	}{
		{
			name:       "upload to a directory",
			setup:      func(d *terraintest.Data) { d.Dirs["/iplant/dir"] = nil },
			target:     "/data/iplant/dir",
			body:       "content",
			wantStatus: 400,
			wantDetail: "Cannot upload file - path is a directory",
		},
		{
			name:       "missing parent",
			setup:      func(d *terraintest.Data) {},
			target:     "/data/iplant/nope/new.txt",
			body:       "content",
			wantStatus: 404,
			wantDetail: "Parent directory '/iplant/nope' not found",
		},
		{
			name: "unwritable parent",
			setup: func(d *terraintest.Data) {
				d.Dirs["/iplant/readonly"] = nil
				d.Perms["/iplant/readonly"] = "read"
			},
			target:     "/data/iplant/readonly/new.txt",
			body:       "content",
			wantStatus: 403,
			wantDetail: "Access denied",
		},
		{
			name: "unwritable existing path",
			setup: func(d *terraintest.Data) {
				d.Files["/iplant/locked.txt"] = []byte("x")
				d.Perms["/iplant/locked.txt"] = "read"
			},
			target:     "/data/iplant/locked.txt",
			body:       "content",
			wantStatus: 403,
			wantDetail: "Access denied",
		},
		{
			name:       "ambiguous request",
			setup:      func(d *terraintest.Data) { d.Dirs["/iplant"] = nil },
			target:     "/data/iplant/new-thing",
			body:       "",
			wantStatus: 400,
			wantDetail: "Cannot determine operation: provide file content or type=directory parameter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newDataEnv(t)
			tt.setup(env.data)

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
		env.data.Files["/iplant/file.txt"] = []byte("x")

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
		if !slices.Contains(env.data.Deleted, "/iplant/file.txt") {
			t.Errorf("deleted = %v", env.data.Deleted)
		}
	})

	t.Run("dry run does not delete", func(t *testing.T) {
		env := newDataEnv(t)
		env.data.Files["/iplant/file.txt"] = []byte("x")

		rec := env.request(t, http.MethodDelete, "/data/iplant/file.txt?dry_run=true", "", nil)
		body := decodeBody(t, rec)
		if body["deleted"] != false || body["dry_run"] != true || body["would_delete"] != true {
			t.Errorf("body = %v", body)
		}
		if len(env.data.Deleted) != 0 {
			t.Errorf("deleted = %v, want none", env.data.Deleted)
		}
	})
}

func TestDataDeleteDirectory(t *testing.T) {
	nonEmpty := []terraintest.DataEntry{{Name: "child.txt"}}

	t.Run("empty dir without recurse", func(t *testing.T) {
		env := newDataEnv(t)
		env.data.Dirs["/iplant/empty"] = nil

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
		env.data.Dirs["/iplant/full"] = nonEmpty

		rec := env.request(t, http.MethodDelete, "/data/iplant/full", "", nil)
		if rec.Code != 400 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		if body := decodeBody(t, rec); body["detail"] != "Directory not empty. Use recurse=true to delete non-empty directories." {
			t.Errorf("detail = %v", body["detail"])
		}
		if len(env.data.Deleted) != 0 {
			t.Errorf("deleted = %v, want none", env.data.Deleted)
		}
	})

	t.Run("dry run on non-empty dir without recurse matches real outcome", func(t *testing.T) {
		env := newDataEnv(t)
		env.data.Dirs["/iplant/full"] = nonEmpty

		rec := env.request(t, http.MethodDelete, "/data/iplant/full?dry_run=true", "", nil)
		if rec.Code != 400 {
			t.Fatalf("status = %d, body %s (dry run should report the same 400)", rec.Code, rec.Body)
		}
	})

	t.Run("recurse reports item_count", func(t *testing.T) {
		env := newDataEnv(t)
		env.data.Dirs["/iplant/full"] = nonEmpty

		rec := env.request(t, http.MethodDelete, "/data/iplant/full?recurse=true", "", nil)
		if rec.Code != 200 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		body := decodeBody(t, rec)
		if body["item_count"] != float64(1) || body["deleted"] != true {
			t.Errorf("body = %v", body)
		}
		if !slices.Contains(env.data.Deleted, "/iplant/full") {
			t.Errorf("deleted = %v", env.data.Deleted)
		}
	})

	t.Run("recurse dry run keeps item_count but does not delete", func(t *testing.T) {
		env := newDataEnv(t)
		env.data.Dirs["/iplant/full"] = nonEmpty

		rec := env.request(t, http.MethodDelete, "/data/iplant/full?recurse=true&dry_run=true", "", nil)
		body := decodeBody(t, rec)
		if body["item_count"] != float64(1) || body["deleted"] != false || body["dry_run"] != true {
			t.Errorf("body = %v", body)
		}
		if len(env.data.Deleted) != 0 {
			t.Errorf("deleted = %v, want none", env.data.Deleted)
		}
	})
}

func TestDataDeleteErrors(t *testing.T) {
	env := newDataEnv(t)
	env.data.Files["/iplant/locked.txt"] = []byte("x")
	env.data.Perms["/iplant/locked.txt"] = "read"

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
