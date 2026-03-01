package bundle

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"infrakey/internal/crypto"
	"infrakey/internal/manifest"
)

type InspectOptions struct {
	BundlePath      string
	IdentityKeyPath string
}

type InspectResult struct {
	Manifest manifest.Manifest
	Entries  []manifest.Entry
	External []manifest.Entry
}

func Inspect(opts InspectOptions) (InspectResult, error) {
	if runtime.GOOS != "linux" {
		return InspectResult{}, fmt.Errorf("linux-only MVP: current platform is %s", runtime.GOOS)
	}

	workDir, err := os.MkdirTemp("", "infrakey-inspect-")
	if err != nil {
		return InspectResult{}, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	tarPath := filepath.Join(workDir, "payload.tar")
	if err := crypto.DecryptFile(opts.BundlePath, opts.IdentityKeyPath, tarPath); err != nil {
		return InspectResult{}, err
	}

	payloadDir := filepath.Join(workDir, "payload")
	if err := os.MkdirAll(payloadDir, 0o700); err != nil {
		return InspectResult{}, fmt.Errorf("create payload dir: %w", err)
	}
	if err := ExtractTar(tarPath, payloadDir); err != nil {
		return InspectResult{}, fmt.Errorf("extract payload: %w", err)
	}

	mf, err := manifest.ReadFromFile(filepath.Join(payloadDir, "manifest.pci.json"))
	if err != nil {
		return InspectResult{}, err
	}

	outside := mf.OutsideRootSet()
	entries := append([]manifest.Entry(nil), mf.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].RestoreRelPath < entries[j].RestoreRelPath })

	external := make([]manifest.Entry, 0, len(outside))
	for _, e := range entries {
		if _, ok := outside[e.ID]; ok {
			external = append(external, e)
		}
	}

	return InspectResult{
		Manifest: mf,
		Entries:  entries,
		External: external,
	}, nil
}
