package apps

import "testing"

func TestFilterApps(t *testing.T) {
	apps := []App{
		{ID: "1", Name: "A", OverallJobType: "interactive", Description: "RNA seq tool", IntegratorName: "bob@x.org", IntegrationDate: "2025-09-30T10:00:00Z"},
		{ID: "2", Name: "B", OverallJobType: "DE", Description: "batch", IntegratorName: "carol@x.org", IntegrationDate: "2024-01-01T00:00:00Z"},
		{ID: "3", Name: "C", OverallJobType: "Interactive", Description: "viewer", IntegrationDate: "2025-09-29 14:30:00"},
	}

	tests := []struct {
		name    string
		filter  ListFilter
		wantIDs []string
		wantErr bool
	}{
		{
			name:    "job type is case-insensitive",
			filter:  ListFilter{JobType: "VICE"},
			wantIDs: []string{"1", "3"},
		},
		{
			name:    "description substring",
			filter:  ListFilter{Description: "seq"},
			wantIDs: []string{"1"},
		},
		{
			name:    "integrator with suffix stripped",
			filter:  ListFilter{Integrator: "bob@x.org", UserSuffix: "@x.org"},
			wantIDs: []string{"1"},
		},
		{
			name:    "integration date after",
			filter:  ListFilter{IntegrationDate: ">2025-01-01"},
			wantIDs: []string{"1", "3"},
		},
		{
			name:    "space-separated datetime parses (parity with python)",
			filter:  ListFilter{IntegrationDate: ">2025-09-29 12:00:00"},
			wantIDs: []string{"1", "3"},
		},
		{
			name:    "invalid date filter errors",
			filter:  ListFilter{IntegrationDate: ">not-a-date"},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FilterApps(apps, tc.filter)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("FilterApps error: %v", err)
			}
			var ids []string
			for _, a := range got {
				ids = append(ids, a.ID)
			}
			if len(ids) != len(tc.wantIDs) {
				t.Fatalf("got ids %v, want %v", ids, tc.wantIDs)
			}
			for i, id := range tc.wantIDs {
				if ids[i] != id {
					t.Errorf("got ids %v, want %v", ids, tc.wantIDs)
					break
				}
			}
		})
	}
}
