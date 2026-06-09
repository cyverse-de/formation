package mcpserver

import (
	"context"
	"encoding/base64"
	"strconv"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

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
	gotLimit      int
	gotOffset     int
}

func (f *fakeApps) GetApp(_ context.Context, _, _, _ string) (*apps.App, error) {
	return &apps.App{Name: "App", Groups: []map[string]any{{"id": "g1"}}, OverallJobType: "Interactive"}, nil
}

// ListApps paginates over f.apps the way the real service does.
func (f *fakeApps) ListApps(_ context.Context, _ string, limit, offset int, _ string) (*apps.AppList, error) {
	f.gotLimit, f.gotOffset = limit, offset
	start := min(offset, len(f.apps))
	end := min(start+limit, len(f.apps))
	return &apps.AppList{Total: len(f.apps), Apps: f.apps[start:end]}, nil
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
func (f *fakeExposer) ExtendTimeLimit(_ context.Context, id string) (*apps.TimeLimit, error) {
	f.calls = append(f.calls, "extend:"+id)
	return &apps.TimeLimit{TimeLimit: "1700000000"}, nil
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

// TestListAppsPassesPaginationUpstream verifies that without client-side-only
// filters, limit/offset go straight to the apps service and its total is used.
func TestListAppsPassesPaginationUpstream(t *testing.T) {
	many := make([]apps.App, 30)
	for i := range many {
		many[i] = apps.App{ID: strconv.Itoa(i), Name: "A"}
	}
	d := newDeps()
	fa := &fakeApps{apps: many}
	d.Apps = fa

	_, out, err := d.listApps(userCtx(), nil, ListAppsIn{Limit: 10, Offset: 20})
	if err != nil {
		t.Fatalf("listApps: %v", err)
	}
	if fa.gotLimit != 10 || fa.gotOffset != 20 {
		t.Errorf("upstream limit/offset = %d/%d, want 10/20", fa.gotLimit, fa.gotOffset)
	}
	if out.Total != 30 || len(out.Apps) != 10 || out.Apps[0].ID != "20" {
		t.Errorf("total=%d apps=%d first=%v", out.Total, len(out.Apps), out.Apps)
	}
}

// TestListAppsFetchesAllPagesForLocalFilters verifies client-side filters see
// apps beyond the service's first page instead of silently truncating at 1000.
func TestListAppsFetchesAllPagesForLocalFilters(t *testing.T) {
	many := make([]apps.App, 1500)
	for i := range many {
		jobType := "DE"
		if i%2 == 0 {
			jobType = "Interactive"
		}
		many[i] = apps.App{ID: strconv.Itoa(i), Name: "A", OverallJobType: jobType}
	}
	d := newDeps()
	d.Apps = &fakeApps{apps: many}

	_, out, err := d.listApps(userCtx(), nil, ListAppsIn{JobType: "VICE", Limit: 10})
	if err != nil {
		t.Fatalf("listApps: %v", err)
	}
	if out.Total != 750 {
		t.Errorf("total = %d, want 750 (Interactive apps across all pages)", out.Total)
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
			d.Apps = &fakeApps{analysis: &apps.Analysis{ID: validUUID, Status: "Running"}}
			_, out, err := d.stopAnalysis(userCtx(), nil, StopAnalysisIn{AnalysisID: validUUID, Operation: tc.op})
			if err != nil {
				t.Fatalf("stop: %v", err)
			}
			if out.Status != tc.wantStatus || out.OutputsSaved != tc.wantSaved {
				t.Errorf("out = %+v", out)
			}
			if tc.op == "extend_time" && out.NewTimeLimit != "1700000000" {
				t.Errorf("new time limit = %q, want 1700000000", out.NewTimeLimit)
			}
			if len(ex.calls) != 1 || ex.calls[0] != tc.wantCallPart+validUUID {
				t.Errorf("calls = %v", ex.calls)
			}
		})
	}
}

// TestStopAnalysisRequiresOwnership verifies the caller cannot control an
// analysis the apps service does not attribute to them.
func TestStopAnalysisRequiresOwnership(t *testing.T) {
	d := newDeps()
	ex := &fakeExposer{}
	d.Exposer = ex
	d.Apps = &fakeApps{} // GetAnalysis returns NotFound
	if _, _, err := d.stopAnalysis(userCtx(), nil, StopAnalysisIn{AnalysisID: validUUID, Operation: "exit"}); err == nil {
		t.Fatal("expected error for analysis not owned by caller")
	}
	if len(ex.calls) != 0 {
		t.Errorf("exposer should not be called, got %v", ex.calls)
	}
}

// TestOpenInBrowserRequiresOwnership verifies the caller cannot resolve the
// URL of an analysis they do not own.
func TestOpenInBrowserRequiresOwnership(t *testing.T) {
	d := newDeps()
	d.Apps = &fakeApps{} // GetAnalysis returns NotFound
	d.Vice = &fakeVice{subdomain: "abc"}
	if _, _, err := d.openInBrowser(userCtx(), nil, OpenInBrowserIn{AnalysisID: validUUID}); err == nil {
		t.Fatal("expected error for analysis not owned by caller")
	}
}

// TestGetAnalysisStatusSkipsURLForTerminal verifies no subdomain resolution is
// attempted for a finished analysis.
func TestGetAnalysisStatusSkipsURLForTerminal(t *testing.T) {
	d := newDeps()
	d.Apps = &fakeApps{analysis: &apps.Analysis{ID: validUUID, Status: "Completed"}}
	d.Vice = &fakeVice{subdomain: "abc", ready: true}
	_, out, err := d.getAnalysisStatus(userCtx(), nil, AnalysisStatusIn{AnalysisID: validUUID})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if out.URL != "" || out.URLReady {
		t.Errorf("expected no URL for terminal analysis, got %+v", out)
	}
}

func TestStopAnalysisRejectsBadOperation(t *testing.T) {
	d := newDeps()
	d.Exposer = &fakeExposer{}
	d.Apps = &fakeApps{analysis: &apps.Analysis{ID: validUUID, Status: "Running"}}
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

// TestIdentityFromRequestExtra verifies the handler reads the per-call identity
// from RequestExtra.TokenInfo (the canonical SDK path), independent of context.
func TestIdentityFromRequestExtra(t *testing.T) {
	d := newDeps()
	fa := &fakeApps{}
	d.Apps = fa

	req := &mcp.CallToolRequest{Extra: &mcp.RequestExtra{
		TokenInfo: &auth.TokenInfo{
			UserID: "bob",
			Extra:  map[string]any{"identity": authz.Identity{DownstreamUsername: "bob"}},
		},
	}}

	// Empty context (no ContextWithIdentity): identity must come from req.
	// launchAppAndWait records the username passed downstream.
	fa.preparedSub = map[string]any{"name": "x"}
	fa.submitResult = &apps.SubmitResult{ID: validUUID, Name: "x", Status: "Submitted"}
	d.Vice = &fakeVice{}
	if _, _, err := d.launchAppAndWait(context.Background(), req, LaunchAppIn{SystemID: "de", AppID: validUUID}); err != nil {
		t.Fatalf("launch with request identity: %v", err)
	}
	if fa.gotUsername != "bob" {
		t.Errorf("downstream username = %q, want bob (from request token)", fa.gotUsername)
	}
}
