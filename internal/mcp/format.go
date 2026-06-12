package mcp

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/cyverse-de/formation/internal/handlers"
)

// looksBinary reports whether a file chunk is non-text. read-chunk returns the
// chunk as a JSON string, so invalid UTF-8 bytes arrive sanitized to U+FFFD and
// no longer signal binary; an embedded NUL, which never appears in text and
// survives the round trip, is the reliable signal. The UTF-8 check remains a
// safety net for any future raw-byte read path.
func looksBinary(content []byte) bool {
	return !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0
}

// The text builders here mirror the Python formation-mcp server's tool output
// (minus the emoji) so prompts written against it keep working.

// strOr returns m[key] as display text, or fallback when absent or nil.
func strOr(m map[string]any, key, fallback string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return fallback
	}
	return fmt.Sprint(v)
}

func formatWhoami(info *handlers.UserInfo) string {
	var b strings.Builder
	b.WriteString("**Current User**\n\n")
	fmt.Fprintf(&b, "Username: %s\n", info.Username)
	if info.FullUsername != "" {
		fmt.Fprintf(&b, "Full Username: %s\n", info.FullUsername)
	}
	if name := strings.TrimSpace(info.FirstName + " " + info.LastName); name != "" {
		fmt.Fprintf(&b, "Name: %s\n", name)
	}
	if info.Email != "" {
		fmt.Fprintf(&b, "Email: %s\n", info.Email)
	}
	if info.HomePath != "" {
		fmt.Fprintf(&b, "Home Directory: `%s`\n", info.HomePath)
	}
	if info.TrashPath != "" {
		fmt.Fprintf(&b, "Trash Directory: `%s`\n", info.TrashPath)
	}
	if info.DefaultOutputFolder != "" {
		fmt.Fprintf(&b, "Default Output Folder: `%s`\n", info.DefaultOutputFolder)
	}
	return b.String()
}

func formatAppsList(result map[string]any) string {
	apps, _ := result["apps"].([]map[string]any)
	if len(apps) == 0 {
		return "No apps found"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %s apps:\n\n", strOr(result, "total", "0"))
	for _, app := range apps {
		fmt.Fprintf(&b, "- **%s**\n", strOr(app, "name", "Unknown"))
		fmt.Fprintf(&b, "  ID: `%s`\n", strOr(app, "id", "N/A"))
		fmt.Fprintf(&b, "  System: %s\n", strOr(app, "system_id", "N/A"))
		if integrator := strOr(app, "integrator_username", ""); integrator != "" {
			fmt.Fprintf(&b, "  Integrator: %s\n", integrator)
		}
		if description := strOr(app, "description", ""); description != "" {
			fmt.Fprintf(&b, "  Description: %s\n", description)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func formatAnalysisStatus(result map[string]any) string {
	var b strings.Builder
	b.WriteString("**Analysis Status**\n\n")
	fmt.Fprintf(&b, "ID: `%s`\n", strOr(result, "analysis_id", ""))
	fmt.Fprintf(&b, "Status: %s\n", strOr(result, "status", ""))
	fmt.Fprintf(&b, "URL Ready: %s\n", strOr(result, "url_ready", "false"))
	if url := strOr(result, "url", ""); url != "" {
		fmt.Fprintf(&b, "URL: %s\n", url)
	}
	return b.String()
}

func formatRunningAnalyses(analyses []map[string]any) string {
	if len(analyses) == 0 {
		return "No running analyses found"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d running analyses:\n\n", len(analyses))
	for _, analysis := range analyses {
		fmt.Fprintf(&b, "- **%s**\n", strOr(analysis, "analysis_id", ""))
		fmt.Fprintf(&b, "  App: %s\n", strOr(analysis, "app_id", ""))
		fmt.Fprintf(&b, "  System: %s\n", strOr(analysis, "system_id", ""))
		fmt.Fprintf(&b, "  Status: %s\n\n", strOr(analysis, "status", ""))
	}
	return b.String()
}

// visibleParams splits an app's parameters into required and optional,
// considering only visible ones (isVisible defaults to true).
func visibleParams(parameters map[string]any) (required, optional []map[string]any) {
	groups, _ := parameters["groups"].([]any)
	for _, rawGroup := range groups {
		group, ok := rawGroup.(map[string]any)
		if !ok {
			continue
		}
		params, _ := group["parameters"].([]any)
		for _, rawParam := range params {
			param, ok := rawParam.(map[string]any)
			if !ok {
				continue
			}
			if visible, ok := param["isVisible"].(bool); ok && !visible {
				continue
			}
			if isRequired, _ := param["required"].(bool); isRequired {
				required = append(required, param)
			} else {
				optional = append(optional, param)
			}
		}
	}
	return required, optional
}

func formatAppParameters(appID string, result map[string]any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**App Parameters: %s**\n\n", appID)
	if jobType := strOr(result, "overall_job_type", ""); jobType != "" {
		fmt.Fprintf(&b, "**Job Type:** %s\n\n", jobType)
	}

	required, optional := visibleParams(result)
	if len(required) > 0 {
		b.WriteString("**Required Parameters:**\n")
		for _, param := range required {
			fmt.Fprintf(&b, "- `%s` (%s)\n", strOr(param, "id", ""), strOr(param, "type", "string"))
			fmt.Fprintf(&b, "  %s\n", strOr(param, "description", "No description"))
			if _, ok := param["defaultValue"]; ok {
				fmt.Fprintf(&b, "  Default: %s\n", strOr(param, "defaultValue", ""))
			}
			b.WriteString("\n")
		}
	} else {
		b.WriteString("**No required parameters**\n\n")
	}

	if len(optional) > 0 {
		fmt.Fprintf(&b, "**Optional Parameters:** %d available\n", len(optional))
	}
	return b.String()
}

// missingRequiredParams lists the required visible parameters absent from the
// provided config, each described by id, name, description, and type.
func missingRequiredParams(parameters map[string]any, provided map[string]any) []map[string]any {
	required, _ := visibleParams(parameters)
	missing := make([]map[string]any, 0, len(required))
	for _, param := range required {
		id := strOr(param, "id", "")
		if _, ok := provided[id]; ok {
			continue
		}
		missing = append(missing, map[string]any{
			"id":          id,
			"name":        strOr(param, "name", id),
			"description": strOr(param, "description", ""),
			"type":        strOr(param, "type", "string"),
		})
	}
	return missing
}

func formatMissingParams(missing []map[string]any) string {
	var b strings.Builder
	b.WriteString("This app requires additional parameters:\n\n")
	for _, param := range missing {
		fmt.Fprintf(&b, "- **%s** (`%s`)\n", strOr(param, "name", ""), strOr(param, "id", ""))
		fmt.Fprintf(&b, "  Type: %s\n", strOr(param, "type", "string"))
		if description := strOr(param, "description", ""); description != "" {
			fmt.Fprintf(&b, "  Description: %s\n", description)
		}
		b.WriteString("\n")
	}
	b.WriteString("\nPlease call this tool again with a `config` parameter containing the required values.\n")
	b.WriteString("Example config format:\n```json\n{\n")
	for _, param := range missing {
		fmt.Fprintf(&b, "  %q: \"value\",\n", strOr(param, "id", ""))
	}
	b.WriteString("}\n```")
	return b.String()
}

func formatInteractiveLaunch(analysisID string, status map[string]any, waitSeconds int) string {
	var b strings.Builder
	b.WriteString("Analysis launched successfully!\n\n")
	fmt.Fprintf(&b, "**Analysis ID:** `%s`\n", analysisID)
	fmt.Fprintf(&b, "**Status:** %s\n", strOr(status, "status", "ready"))
	fmt.Fprintf(&b, "**URL:** %s\n", strOr(status, "url", ""))
	fmt.Fprintf(&b, "**Wait time:** %ds\n", waitSeconds)
	return b.String()
}

func formatLaunchPollFailure(analysisID string, err error) string {
	var b strings.Builder
	b.WriteString("Analysis launched successfully!\n\n")
	fmt.Fprintf(&b, "**Analysis ID:** `%s`\n", analysisID)
	b.WriteString("**Status:** unknown — status polling failed\n")
	fmt.Fprintf(&b, "\nStatus check error: %s\n", err)
	b.WriteString("Use get_analysis_status to check readiness.\n")
	return b.String()
}

func formatBatchLaunch(analysisID, jobType string) string {
	var b strings.Builder
	b.WriteString("Analysis launched successfully!\n\n")
	fmt.Fprintf(&b, "**Analysis ID:** `%s`\n", analysisID)
	b.WriteString("**Status:** submitted\n")
	fmt.Fprintf(&b, "**Job Type:** %s\n", jobType)
	b.WriteString("\nBatch job submitted - check status later\n")
	return b.String()
}

func formatBrowse(result *handlers.BrowseResult) string {
	var b strings.Builder
	if result.Type == handlers.TypeCollection {
		fmt.Fprintf(&b, "**Directory:** `%s`\n\n", result.Path)
		var dirs, files []handlers.Entry
		for _, entry := range result.Entries {
			if entry.Type == handlers.TypeCollection {
				dirs = append(dirs, entry)
			} else {
				files = append(files, entry)
			}
		}
		switch {
		case len(dirs) == 0 && len(files) == 0:
			b.WriteString("*(empty directory)*")
		default:
			if len(dirs) > 0 {
				b.WriteString("**Directories:**\n")
				for _, d := range dirs {
					fmt.Fprintf(&b, "- %s\n", d.Name)
				}
				b.WriteString("\n")
			}
			if len(files) > 0 {
				b.WriteString("**Files:**\n")
				for _, f := range files {
					fmt.Fprintf(&b, "- %s\n", f.Name)
				}
			}
		}
	} else if looksBinary(result.Content) {
		fmt.Fprintf(&b, "**Unsupported file type:** %d bytes — formation serves text content only; this file is not text and cannot be retrieved.", len(result.Content))
	} else {
		fmt.Fprintf(&b, "**File Content:**\n\n```\n%s\n```", result.Content)
		if result.Truncated {
			b.WriteString("\n\n*(truncated; each read adds the returned bytes to the conversation — page with offset/limit)*")
		}
	}

	if len(result.Metadata) > 0 {
		b.WriteString("\n\n**Metadata:**\n")
		for _, avu := range result.Metadata {
			value := avu.Value
			if avu.Unit != "" {
				value += "," + avu.Unit
			}
			fmt.Fprintf(&b, "- %s: %s\n", avu.Attr, value)
		}
	}
	return b.String()
}

func formatDelete(result map[string]any, recurse bool) string {
	path := strOr(result, "path", "")
	isCollection := strOr(result, "type", "") == handlers.TypeCollection
	itemCount := strOr(result, "item_count", "")

	if dryRun, _ := result["dry_run"].(bool); dryRun {
		out := fmt.Sprintf("Dry-run: Would delete `%s`", path)
		if isCollection && itemCount != "" {
			out += fmt.Sprintf(" (%s items)", itemCount)
		}
		return out
	}

	action := "Deleted"
	if recurse && isCollection {
		action = "Deleted (recursive)"
	}
	out := fmt.Sprintf("%s: `%s`", action, path)
	if itemCount != "" {
		out += fmt.Sprintf(" (%s items)", itemCount)
	}
	return out
}
