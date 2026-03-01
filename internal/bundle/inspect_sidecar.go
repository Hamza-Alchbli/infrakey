package bundle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"infrakey/internal/crypto"
	"infrakey/internal/manifest"
)

const inspectSidecarName = "inspect.age"

func inspectSidecarPathForBundle(bundlePath string) (string, bool) {
	chunkDir := chunkDirForBundle(bundlePath)
	if st, err := os.Stat(chunkDir); err == nil && st.IsDir() {
		return filepath.Join(chunkDir, inspectSidecarName), true
	}
	return bundlePath + ".inspect.age", false
}

func writeInspectSidecar(bundlePath string, chunked bool, mf manifest.Manifest, recipient string) error {
	var sidecarPath string
	if chunked {
		sidecarPath = filepath.Join(chunkDirForBundle(bundlePath), inspectSidecarName)
	} else {
		sidecarPath = bundlePath + ".inspect.age"
	}
	dir := filepath.Dir(sidecarPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create inspect sidecar dir: %w", err)
	}
	encoded, err := manifest.Encode(mf)
	if err != nil {
		return err
	}
	if err := crypto.EncryptBytes(encoded, sidecarPath, recipient); err != nil {
		return fmt.Errorf("write inspect sidecar: %w", err)
	}
	if err := os.Chmod(sidecarPath, 0o644); err != nil {
		return fmt.Errorf("chmod inspect sidecar: %w", err)
	}
	return nil
}

func tryReadInspectSidecar(bundlePath, identityKeyPath string) (manifest.Manifest, bool, error) {
	sidecarPath, inChunkDir := inspectSidecarPathForBundle(bundlePath)
	if _, err := os.Stat(sidecarPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// If chunk dir exists but sidecar is missing, this bundle was created before sidecars.
			if inChunkDir {
				return manifest.Manifest{}, false, nil
			}
			return manifest.Manifest{}, false, nil
		}
		return manifest.Manifest{}, false, fmt.Errorf("stat inspect sidecar: %w", err)
	}
	b, err := crypto.DecryptFileToBytes(sidecarPath, identityKeyPath)
	if err != nil {
		return manifest.Manifest{}, false, err
	}
	mf, err := manifest.ReadFromBytes(b)
	if err != nil {
		return manifest.Manifest{}, false, err
	}
	return mf, true, nil
}
