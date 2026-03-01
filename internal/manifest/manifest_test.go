package manifest

import "testing"

func TestManifestValidate(t *testing.T) {
	m := Manifest{
		PCIVersion: CurrentPCIVersion,
		SnapshotID: "snap-1",
		CreatedAt:  "2026-02-28T10:00:00Z",
		SourceRoot: "/srv/apps",
		Entries: []Entry{
			{
				ID:            "entry1",
				Kind:          KindCompose,
				SourceAbsPath: "/srv/apps/compose.yml",
				RestoreRelPath: "compose.yml",
				SHA256:        "abc",
				Mode:          "0644",
			},
		},
		OutsideRootEntries: []string{"entry1"},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("expected valid manifest, got error: %v", err)
	}
}

func TestManifestValidateUnknownOutsideEntry(t *testing.T) {
	m := Manifest{
		PCIVersion: CurrentPCIVersion,
		SnapshotID: "snap-1",
		CreatedAt:  "2026-02-28T10:00:00Z",
		SourceRoot: "/srv/apps",
		Entries: []Entry{
			{
				ID:            "entry1",
				Kind:          KindCompose,
				SourceAbsPath: "/srv/apps/compose.yml",
				RestoreRelPath: "compose.yml",
				SHA256:        "abc",
				Mode:          "0644",
			},
		},
		OutsideRootEntries: []string{"missing"},
	}
	if err := m.Validate(); err == nil {
		t.Fatalf("expected validation error")
	}
}
