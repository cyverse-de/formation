package apps

// App is the subset of an apps-service app record that Formation surfaces.
type App struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Description     string           `json:"description"`
	Version         string           `json:"version"`
	IntegratorName  string           `json:"integrator_name"`
	IntegrationDate string           `json:"integration_date"`
	EditedDate      string           `json:"edited_date"`
	SystemID        string           `json:"system_id"`
	OverallJobType  string           `json:"overall_job_type"`
	Groups          []map[string]any `json:"groups"`
}

// AppList is the apps-service response for a list of apps. Total is the full
// match count, which can exceed len(Apps) when the result is paginated.
type AppList struct {
	Total int   `json:"total"`
	Apps  []App `json:"apps"`
}

// Analysis is the subset of an analysis record that Formation surfaces.
type Analysis struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	AppID    string `json:"app_id"`
	SystemID string `json:"system_id"`
	Status   string `json:"status"`
}

// analysisList is the apps-service response for a list of analyses.
type analysisList struct {
	Analyses []Analysis `json:"analyses"`
}

// SubmitResult is the apps-service response to an analysis submission.
type SubmitResult struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// TimeLimit is the app-exposer response carrying an analysis's planned end
// time as a Unix-epoch string.
type TimeLimit struct {
	TimeLimit string `json:"time_limit"`
}

// AsyncData is the app-exposer response carrying the asynchronously generated
// subdomain for a VICE analysis.
type AsyncData struct {
	AnalysisID string `json:"analysisID"`
	Subdomain  string `json:"subdomain"`
	IPAddr     string `json:"ipAddr"`
}

// analysisFilter is one entry in the apps-service JSON filter array.
type analysisFilter struct {
	Field string `json:"field"`
	Value string `json:"value"`
}
