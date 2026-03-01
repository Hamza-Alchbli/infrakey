package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
)

const CurrentPCIVersion = "0.1"

const (
	KindCompose  = "compose"
	KindEnv      = "env"
	KindSecret   = "secret"
	KindConfig   = "config"
	KindCert     = "cert"
	KindExternal = "external"
)

type Entry struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	SourceAbsPath string `json:"sourceAbsPath"`
	SourceRelPath string `json:"sourceRelPath,omitempty"`
	RestoreRelPath string `json:"restoreRelPath"`
	SHA256        string `json:"sha256"`
	Mode          string `json:"mode"`
}

type PathReplacement struct {
	OriginalPath string `json:"originalPath"`
	RestoredPath string `json:"restoredPath"`
}

type ComposeRewrite struct {
	ComposeEntryID string            `json:"composeEntryId"`
	Replacements   []PathReplacement `json:"replacements"`
}

type Manifest struct {
	PCIVersion       string           `json:"pciVersion"`
	SnapshotID       string           `json:"snapshotId"`
	CreatedAt        string           `json:"createdAt"`
	SourceRoot       string           `json:"sourceRoot"`
	Entries          []Entry          `json:"entries"`
	ComposeRewrites  []ComposeRewrite `json:"composeRewrites"`
	OutsideRootEntries []string       `json:"outsideRootEntries"`
}

func (m Manifest) Validate() error {
	if m.PCIVersion != CurrentPCIVersion {
		return fmt.Errorf("unsupported pciVersion %q", m.PCIVersion)
	}
	if m.SnapshotID == "" {
		return errors.New("snapshotId is required")
	}
	if m.CreatedAt == "" {
		return errors.New("createdAt is required")
	}
	if m.SourceRoot == "" {
		return errors.New("sourceRoot is required")
	}
	ids := map[string]struct{}{}
	for _, e := range m.Entries {
		if e.ID == "" {
			return errors.New("entry id is required")
		}
		if _, ok := ids[e.ID]; ok {
			return fmt.Errorf("duplicate entry id %q", e.ID)
		}
		ids[e.ID] = struct{}{}
		if e.Kind == "" || e.SourceAbsPath == "" || e.RestoreRelPath == "" || e.SHA256 == "" || e.Mode == "" {
			return fmt.Errorf("entry %q is missing required fields", e.ID)
		}
	}
	for _, id := range m.OutsideRootEntries {
		if _, ok := ids[id]; !ok {
			return fmt.Errorf("outsideRootEntries references unknown entry %q", id)
		}
	}
	return nil
}

func (m Manifest) OutsideRootSet() map[string]struct{} {
	set := make(map[string]struct{}, len(m.OutsideRootEntries))
	for _, id := range m.OutsideRootEntries {
		set[id] = struct{}{}
	}
	return set
}

func (m *Manifest) SortStable() {
	sort.Slice(m.Entries, func(i, j int) bool {
		return m.Entries[i].RestoreRelPath < m.Entries[j].RestoreRelPath
	})
	for i := range m.ComposeRewrites {
		sort.Slice(m.ComposeRewrites[i].Replacements, func(a, b int) bool {
			return m.ComposeRewrites[i].Replacements[a].OriginalPath < m.ComposeRewrites[i].Replacements[b].OriginalPath
		})
	}
	sort.Slice(m.ComposeRewrites, func(i, j int) bool {
		return m.ComposeRewrites[i].ComposeEntryID < m.ComposeRewrites[j].ComposeEntryID
	})
	sort.Strings(m.OutsideRootEntries)
}

func WriteToFile(path string, m Manifest) error {
	m.SortStable()
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

func ReadFromFile(path string) (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(path)
	if err != nil {
		return m, fmt.Errorf("read manifest: %w", err)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("parse manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return m, err
	}
	return m, nil
}
