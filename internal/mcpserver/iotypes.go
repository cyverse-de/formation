package mcpserver

import (
	"github.com/cyverse-de/formation/internal/apps"
	"github.com/cyverse-de/formation/internal/datastore"
)

// ListAppsIn are the inputs to the list_apps tool.
type ListAppsIn struct {
	Limit           int    `json:"limit,omitempty" jsonschema:"maximum number of apps to return (1-1000, default 100)"`
	Offset          int    `json:"offset,omitempty" jsonschema:"number of apps to skip for pagination"`
	Name            string `json:"name,omitempty" jsonschema:"filter by app name (search term)"`
	Description     string `json:"description,omitempty" jsonschema:"filter by substring of the app description"`
	Integrator      string `json:"integrator,omitempty" jsonschema:"filter by integrator username"`
	IntegrationDate string `json:"integration_date,omitempty" jsonschema:"filter by integration date, e.g. \">2025-09-29\""`
	EditedDate      string `json:"edited_date,omitempty" jsonschema:"filter by last-edited date, e.g. \"<=2024-12-31\""`
	JobType         string `json:"job_type,omitempty" jsonschema:"filter by job type: VICE, DE, OSG, or Tapis"`
}

// AppOut is a single app in the list_apps result.
type AppOut struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Description        string `json:"description"`
	Version            string `json:"version"`
	IntegratorUsername string `json:"integrator_username"`
	IntegrationDate    string `json:"integration_date"`
	EditedDate         string `json:"edited_date"`
	SystemID           string `json:"system_id"`
	OverallJobType     string `json:"overall_job_type"`
}

// ListAppsOut is the list_apps result.
type ListAppsOut struct {
	Total int      `json:"total"`
	Apps  []AppOut `json:"apps"`
}

// GetAppParametersIn are the inputs to the get_app_parameters tool.
type GetAppParametersIn struct {
	SystemID string `json:"system_id" jsonschema:"system identifier from the app listing"`
	AppID    string `json:"app_id" jsonschema:"app UUID"`
}

// GetAppParametersOut is the get_app_parameters result. Groups is an opaque
// list of parameter-group objects passed through from the apps service.
type GetAppParametersOut struct {
	Groups         []map[string]any `json:"groups"`
	OverallJobType string           `json:"overall_job_type"`
}

// LaunchAppIn are the inputs to the launch_app_and_wait tool.
type LaunchAppIn struct {
	SystemID   string         `json:"system_id" jsonschema:"system identifier from the app listing"`
	AppID      string         `json:"app_id" jsonschema:"app UUID"`
	Submission map[string]any `json:"submission,omitempty" jsonschema:"analysis submission parameters; defaults are filled in automatically"`
	OutputZone string         `json:"output_zone,omitempty" jsonschema:"iRODS zone for the output directory; defaults to the configured zone"`
}

// LaunchAppOut is the launch_app_and_wait result.
type LaunchAppOut struct {
	AnalysisID string `json:"analysis_id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	URL        string `json:"url,omitempty"`
}

// AnalysisStatusIn are the inputs to the get_analysis_status tool.
type AnalysisStatusIn struct {
	AnalysisID string `json:"analysis_id" jsonschema:"analysis UUID"`
}

// AnalysisStatusOut is the get_analysis_status result.
type AnalysisStatusOut struct {
	AnalysisID      string             `json:"analysis_id"`
	Status          string             `json:"status"`
	URLReady        bool               `json:"url_ready"`
	URL             string             `json:"url,omitempty"`
	URLCheckDetails *apps.ProbeDetails `json:"url_check_details,omitempty"`
}

// ListRunningAnalysesIn are the inputs to the list_running_analyses tool.
type ListRunningAnalysesIn struct {
	Status string `json:"status,omitempty" jsonschema:"status filter (default Running): Running, Completed, Failed, Submitted, Canceled"`
}

// AnalysisOut is a single analysis in the list_running_analyses result.
type AnalysisOut struct {
	AnalysisID string `json:"analysis_id"`
	Name       string `json:"name"`
	AppID      string `json:"app_id"`
	SystemID   string `json:"system_id"`
	Status     string `json:"status"`
}

// ListRunningAnalysesOut is the list_running_analyses result.
type ListRunningAnalysesOut struct {
	Analyses []AnalysisOut `json:"analyses"`
}

// StopAnalysisIn are the inputs to the stop_analysis tool.
type StopAnalysisIn struct {
	AnalysisID string `json:"analysis_id" jsonschema:"analysis UUID"`
	Operation  string `json:"operation" jsonschema:"control operation: save_and_exit, exit, or extend_time"`
}

// StopAnalysisOut is the stop_analysis result.
type StopAnalysisOut struct {
	AnalysisID   string `json:"analysis_id"`
	Operation    string `json:"operation"`
	Status       string `json:"status"`
	OutputsSaved bool   `json:"outputs_saved"`
	NewTimeLimit string `json:"new_time_limit,omitempty" jsonschema:"for extend_time, the new planned end time as a Unix-epoch string"`
}

// OpenInBrowserIn are the inputs to the open_in_browser tool.
type OpenInBrowserIn struct {
	AnalysisID string `json:"analysis_id" jsonschema:"analysis UUID"`
}

// OpenInBrowserOut is the open_in_browser result.
type OpenInBrowserOut struct {
	URL   string `json:"url"`
	Ready bool   `json:"ready"`
}

// MetaIn is an AVU metadata triple supplied to data tools.
type MetaIn struct {
	Attribute string `json:"attribute" jsonschema:"AVU attribute name"`
	Value     string `json:"value" jsonschema:"AVU value"`
	Units     string `json:"units,omitempty" jsonschema:"optional AVU units"`
}

// BrowseDataIn are the inputs to the browse_data tool.
type BrowseDataIn struct {
	Path            string `json:"path" jsonschema:"full iRODS path to a file or directory"`
	Offset          int    `json:"offset,omitempty" jsonschema:"byte offset when reading a file"`
	Limit           int    `json:"limit,omitempty" jsonschema:"maximum number of bytes to read from a file (capped at 8 MiB per call; page larger files with offset)"`
	IncludeMetadata bool   `json:"include_metadata,omitempty" jsonschema:"include AVU metadata in the result"`
	AVUDelimiter    string `json:"avu_delimiter,omitempty" jsonschema:"delimiter between metadata value and units (default ',')"`
}

// BrowseDataOut is the browse_data result. For directories, Contents is set;
// for files, Content holds the base64-encoded bytes.
type BrowseDataOut struct {
	Path     string            `json:"path"`
	Type     string            `json:"type"`
	Contents []datastore.Entry `json:"contents,omitempty"`
	Content  string            `json:"content,omitempty" jsonschema:"file content, base64-encoded"`
	Size     int64             `json:"size,omitempty"`
	Offset   int               `json:"offset,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// CreateDirectoryIn are the inputs to the create_directory tool.
type CreateDirectoryIn struct {
	Path     string   `json:"path" jsonschema:"full iRODS path of the directory to create"`
	Metadata []MetaIn `json:"metadata,omitempty" jsonschema:"AVU metadata to set on the directory"`
}

// UploadFileIn are the inputs to the upload_file tool.
type UploadFileIn struct {
	Path            string   `json:"path" jsonschema:"full iRODS path of the file to create or overwrite"`
	Content         string   `json:"content" jsonschema:"file content, base64-encoded"`
	Metadata        []MetaIn `json:"metadata,omitempty" jsonschema:"AVU metadata to set on the file"`
	ReplaceMetadata bool     `json:"replace_metadata,omitempty" jsonschema:"replace existing values for these attributes instead of adding"`
}

// SetMetadataIn are the inputs to the set_metadata tool.
type SetMetadataIn struct {
	Path     string   `json:"path" jsonschema:"full iRODS path"`
	Metadata []MetaIn `json:"metadata" jsonschema:"AVU metadata to set"`
	Replace  bool     `json:"replace,omitempty" jsonschema:"replace existing values for these attributes instead of adding"`
}

// WriteOut is the result of create_directory, upload_file, and set_metadata.
type WriteOut struct {
	Path    string `json:"path"`
	Type    string `json:"type"`
	Created bool   `json:"created"`
}

// DeleteDataIn are the inputs to the delete_data tool.
type DeleteDataIn struct {
	Path    string `json:"path" jsonschema:"full iRODS path to delete"`
	Recurse bool   `json:"recurse,omitempty" jsonschema:"allow deleting non-empty directories"`
	DryRun  bool   `json:"dry_run,omitempty" jsonschema:"preview the deletion without performing it"`
}

// DeleteDataOut is the delete_data result.
type DeleteDataOut struct {
	Path        string `json:"path"`
	Type        string `json:"type"`
	WouldDelete bool   `json:"would_delete"`
	Deleted     bool   `json:"deleted"`
	DryRun      bool   `json:"dry_run"`
	ItemCount   int    `json:"item_count,omitempty"`
}
