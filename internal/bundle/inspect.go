package bundle

import (
	"archive/tar"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

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

	var mf manifest.Manifest
	usedSidecar := false
	if sidecarManifest, ok, err := tryReadInspectSidecar(opts.BundlePath, opts.IdentityKeyPath); err != nil {
		return InspectResult{}, err
	} else if ok {
		mf = sidecarManifest
		usedSidecar = true
	} else {
		bundleReader, _, err := OpenBundleReader(opts.BundlePath)
		if err != nil {
			return InspectResult{}, err
		}
		defer bundleReader.Close()
		if err := crypto.DecryptFromReader(bundleReader, opts.IdentityKeyPath, func(r io.Reader) error {
			parsed, err := readManifestFromTarReader(r)
			if err != nil {
				return err
			}
			mf = parsed
			return nil
		}); err != nil {
			return InspectResult{}, err
		}
	}
	if !usedSidecar {
		// Best-effort cache sidecar for subsequent fast inspect.
		if recipient, err := crypto.RecipientFromIdentity(opts.IdentityKeyPath); err == nil {
			_, inChunkDir := inspectSidecarPathForBundle(opts.BundlePath)
			_ = writeInspectSidecar(opts.BundlePath, inChunkDir, mf, recipient)
		}
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

func readManifestFromTarReader(r io.Reader) (manifest.Manifest, error) {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return manifest.Manifest{}, fmt.Errorf("read tar: %w", err)
		}

		cleanName := filepath.Clean(filepath.FromSlash(hdr.Name))
		if strings.HasPrefix(cleanName, ".."+string(filepath.Separator)) || cleanName == ".." {
			return manifest.Manifest{}, fmt.Errorf("tar entry escapes destination: %q", hdr.Name)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if cleanName != "manifest.pci.json" {
			if _, err := io.Copy(io.Discard, tr); err != nil {
				return manifest.Manifest{}, fmt.Errorf("drain tar entry %q: %w", hdr.Name, err)
			}
			continue
		}

		b, err := io.ReadAll(tr)
		if err != nil {
			return manifest.Manifest{}, fmt.Errorf("read manifest entry: %w", err)
		}
		parsed, err := manifest.ReadFromBytes(b)
		if err != nil {
			return manifest.Manifest{}, err
		}
		return parsed, nil
	}
	return manifest.Manifest{}, fmt.Errorf("manifest.pci.json not found in payload")
}
