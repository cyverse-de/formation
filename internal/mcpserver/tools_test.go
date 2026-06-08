package mcpserver

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/cyverse-de/formation/internal/apperr"
	"github.com/cyverse-de/formation/internal/apps"
	"github.com/cyverse-de/formation/internal/authz"
	"github.com/cyverse-de/formation/internal/datastore"
)

// fakeApps records calls and returns canned data.
type fakeApps struct {
	apps         []apps.App
	analysis     *apps.Analysis
	analyses     []apps.Analysis
	submitResult *apps.SubmitResult
	preparedSub  map[string]any
	preparedMail string

	gotSubmission map[string]any
	gotUsername   string
	gotEmail      string
}

func (f *fakeApps) GetApp(_ context.Context, _, _, _ string) (*apps.App, error) {
	return &apps.App{Name: "App", Groups: []map[string]any{{"id": "g1"}}, OverallJobType: "Interactive"}, nil
}
func (f *fakeApps) ListApps(_ context.Context, _ string, _, _ int, _ string) (*apps.AppList, error) {
	return &apps.AppList{Apps: f.apps}, nil
}
func (f *fakeApps) SubmitAnalysis(_ context.Context, sub map[string]any, username, email string) (*apps.SubmitResult, error) {
	f.gotSubmission, f.gotUsername, f.gotEmail = sub, username, email
	return f.submitResult, nil
}
func (f *fakeApps) GetAnalysis(_ context.Context, id, _ string) (*apps.Analysis, error) {
	if f.analysis == nil {
		return nil, apperr.NotFound("Analysis", id)
	}
	return f.analysis, nil
}
func (f *fakeApps) ListAnalyses(_ context.Context, _, _ string) ([]apps.Analysis, error) {
	return f.analyses, nil
}
func (f *fakeApps) PrepareSubmission(_ context.Context, _ apps.PrepareInput) (map[string]any, string, error) {
	return f.preparedSub, f.preparedMail, nil
}

type fakeExposer struct{ calls []string }

func (f *fakeExposer) SaveAndExit(_ context.Context, id string) error {
	f.calls = append(f.calls, "save:"+id)
	return nil
}
func (f *fakeExposer) ExitWithoutSave(_ context.Context, id string) error {
	f.calls = append(f.calls, "exit:"+id)
	return nil
}
func (f *fakeExposer) ExtendTimeLimit(_ context.Context, id string) error {
	f.calls = append(f.calls, "extend:"+id)
	return nil
}

type fakeVice struct {
	subdomain string
	ready     bool
}

func (f *fakeVice) ResolveSubdomain(_ context.Context, _ string) string { return f.subdomain }
func (f *fakeVice) CheckURLReady(_ context.Context, _ string) (bool, apps.ProbeDetails) {
	return f.ready, apps.ProbeDetails{StatusCode: 200, Attempt: 1}
}
func (f *fakeVice) URLFor(subdomain string) string { return "https://" + subdomain + ".cyverse.run" }

type fakeData struct {
	browse *datastore.BrowseResult
	del    *datastore.DeleteResult
	write  *datastore.WriteResult

	gotMeta    []datastore.AVU
	gotReplace bool
}

func (f *fakeData) Browse(_ string, _ string, _, _ int, _ bool, _ string) (*datastore.BrowseResult, error) {
	return f.browse, nil
}
func (f *fakeData) CreateDirectory(_ string, _ string, meta []datastore.AVU) (*datastore.WriteResult, error) {
	f.gotMeta = meta
	return f.write, nil
}
func (f *fakeData) UploadFile(_ string, _ string, _ []byte, meta []datastore.AVU, replace bool) (*datastore.WriteResult, error) {
	f.gotMeta, f.gotReplace = meta, replace
	return f.write, nil
}
func (f *fakeData) SetMetadata(_ string, _ string, meta []datastore.AVU, replace bool) (*datastore.WriteResult, error) {
	f.gotMeta, f.gotReplace = meta, replace
	return f.write, nil
}
func (f *fakeData) Delete(_ string, _ string, _, _ bool) (*datastore.DeleteResult, error) {
	return f.del, nil
}

func userCtx() context.Context {
	return authz.ContextWithIdentity(context.Background(), authz.Identity{
		DownstreamUsername: "alice", Email: "alice@example.org",
	})
}

func newDeps() *Deps {
	return &Deps{
		Logger:     nil,
		UserSuffix: "@iplantcollaborative.org",
		OutputZone: "iplant",
		Now:        func() time.Time { return time.Unix(0, 0) },
	}
}

const validUUID = "12345678-1234-1234-1234-123456789abc"

func TestListAppsFiltersAndPaginates(t *testing.T) {
	d := newDeps()
	d.Apps = &fakeApps{apps: []apps.App{
		{ID: "1", Name: "A", OverallJobType: "Interactive", IntegratorName: "bob@iplantcollaborative.org"},
		{ID: "2", Name: "B", OverallJobType: "DE"},
		{ID: "3", Name: "C", OverallJobType: "Interactive"},
	}}

	_, out, err := d.listApps(userCtx(), nil, ListAppsIn{JobType: "VICE", Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("listApps: %v", err)
	}
	if out.Total != 2 {
		t.Errorf("total = %d, want 2 (two Interactive apps)", out.Total)
	}
	if len(out.Apps) != 1 || out.Apps[0].ID != "3" {
		t.Errorf("pagination wrong: %+v", out.Apps)
	}
}

func TestListAppsStripsIntegratorSuffix(t *testing.T) {
	d := newDeps()
	d.Apps = &fakeApps{apps: []apps.App{
		{ID: "1", Name: "A", IntegratorName: "bob@iplantcollaborative.org"},
	}}
	_, out, err := d.listApps(userCtx(), nil, ListAppsIn{})
	if err != nil {
		t.Fatalf("listApps: %v", err)
	}
	if out.Apps[0].IntegratorUsername != "bob" {
		t.Errorf("integrator = %q, want bob", out.Apps[0].IntegratorUsername)
	}
}

func TestListAppsValidatesPagination(t *testing.T) {
	d := newDeps()
	d.Apps = &fakeApps{}
	if _, _, err := d.listApps(userCtx(), nil, ListAppsIn{Limit: 5000}); err == nil {
		t.Error("expected validation error for limit > 1000")
	}
}

func TestLaunchAppComposesURL(t *testing.T) {
	d := newDeps()
	d.Apps = &fakeApps{
		preparedSub:  map[string]any{"name": "myrun"},
		preparedMail: "alice@example.org",
		submitResult: &apps.SubmitResult{ID: validUUID, Name: "myrun", Status: "Submitted"},
	}
	d.Vice = &fakeVice{subdomain: "abc"}

	_, out, err := d.launchAppAndWait(userCtx(), nil, LaunchAppIn{SystemID: "de", AppID: validUUID})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if out.URL != "https://abc.cyverse.run" {
		t.Errorf("url = %q", out.URL)
	}
	if out.AnalysisID != validUUID || out.Name != "myrun" {
		t.Errorf("out = %+v", out)
	}
}

func TestLaunchAppRejectsBadUUID(t *testing.T) {
	d := newDeps()
	d.Apps = &fakeApps{}
	if _, _, err := d.launchAppAndWait(userCtx(), nil, LaunchAppIn{SystemID: "de", AppID: "not-a-uuid"}); err == nil {
		t.Error("expected validation error for bad UUID")
	}
}

func TestGetAnalysisStatusProbesURL(t *testing.T) {
	d := newDeps()
	d.Apps = &fakeApps{analysis: &apps.Analysis{ID: validUUID, Status: "Running"}}
	d.Vice = &fakeVice{subdomain: "abc", ready: true}

	_, out, err := d.getAnalysisStatus(userCtx(), nil, AnalysisStatusIn{AnalysisID: validUUID})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !out.URLReady || out.URL != "https://abc.cyverse.run" || out.Status != "Running" {
		t.Errorf("out = %+v", out)
	}
	if out.URLCheckDetails == nil || out.URLCheckDetails.StatusCode != 200 {
		t.Errorf("expected url check details, got %+v", out.URLCheckDetails)
	}
}

func TestStopAnalysisOperations(t *testing.T) {
	tests := []struct {
		op           string
		wantStatus   string
		wantSaved    bool
		wantCallPart string
	}{
		{"save_and_exit", "terminated", true, "save:"},
		{"exit", "terminated", false, "exit:"},
		{"extend_time", "extended", false, "extend:"},
	}
	for _, tc := range tests {
		t.Run(tc.op, func(t *testing.T) {
			d := newDeps()
			ex := &fakeExposer{}
			d.Exposer = ex
			_, out, err := d.stopAnalysis(userCtx(), nil, StopAnalysisIn{AnalysisID: validUUID, Operation: tc.op})
			if err != nil {
				t.Fatalf("stop: %v", err)
			}
			if out.Status != tc.wantStatus || out.OutputsSaved != tc.wantSaved {
				t.Errorf("out = %+v", out)
			}
			if len(ex.calls) != 1 || ex.calls[0] != tc.wantCallPart+validUUID {
				t.Errorf("calls = %v", ex.calls)
			}
		})
	}
}

func TestStopAnalysisRejectsBadOperation(t *testing.T) {
	d := newDeps()
	d.Exposer = &fakeExposer{}
	if _, _, err := d.stopAnalysis(userCtx(), nil, StopAnalysisIn{AnalysisID: validUUID, Operation: "nope"}); err == nil {
		t.Error("expected validation error for invalid operation")
	}
}

func TestBrowseDataDirectory(t *testing.T) {
	d := newDeps()
	d.Data = &fakeData{browse: &datastore.BrowseResult{
		Path: "/iplant/home/alice", Type: "collection",
		Contents: []datastore.Entry{{Name: "f.txt", Type: "data_object"}},
	}}
	_, out, err := d.browseData(userCtx(), nil, BrowseDataIn{Path: "/iplant/home/alice"})
	if err != nil {
		t.Fatalf("browse: %v", err)
	}
	if out.Type != "collection" || len(out.Contents) != 1 || out.Contents[0].Name != "f.txt" {
		t.Errorf("out = %+v", out)
	}
}

func TestUploadFilePassesMetadata(t *testing.T) {
	d := newDeps()
	fd := &fakeData{write: &datastore.WriteResult{Path: "/p", Type: "data_object", Created: true}}
	d.Data = fd
	_, out, err := d.uploadFile(userCtx(), nil, UploadFileIn{
		Path:            "/p",
		Content:         base64.StdEncoding.EncodeToString([]byte("hi")),
		Metadata:        []MetaIn{{Attribute: "a", Value: "v", Units: "u"}},
		ReplaceMetadata: true,
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if !out.Created {
		t.Error("expected created")
	}
	if len(fd.gotMeta) != 1 || fd.gotMeta[0].Attribute != "a" || !fd.gotReplace {
		t.Errorf("metadata not forwarded: %+v replace=%v", fd.gotMeta, fd.gotReplace)
	}
}

func TestDeleteDataDryRun(t *testing.T) {
	d := newDeps()
	d.Data = &fakeData{del: &datastore.DeleteResult{
		Path: "/p", Type: "collection", WouldDelete: true, DryRun: true, ItemCount: 3,
	}}
	_, out, err := d.deleteData(userCtx(), nil, DeleteDataIn{Path: "/p", DryRun: true, Recurse: true})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !out.WouldDelete || out.Deleted || !out.DryRun || out.ItemCount != 3 {
		t.Errorf("out = %+v", out)
	}
}

func TestUnauthenticatedRejected(t *testing.T) {
	d := newDeps()
	d.Apps = &fakeApps{}
	if _, _, err := d.listApps(context.Background(), nil, ListAppsIn{}); err == nil {
		t.Error("expected unauthenticated error without identity in context")
	}
}
