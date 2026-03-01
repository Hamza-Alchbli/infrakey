package crypto

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func EnsureAgeInstalled() error {
	if _, err := exec.LookPath("age"); err != nil {
		return fmt.Errorf("age binary not found in PATH")
	}
	return nil
}

func EnsureAgeKeygenInstalled() error {
	if _, err := exec.LookPath("age-keygen"); err != nil {
		return fmt.Errorf("age-keygen binary not found in PATH")
	}
	return nil
}

func GenerateIdentity(identityPath string) (string, error) {
	if err := EnsureAgeKeygenInstalled(); err != nil {
		return "", err
	}
	cmd := exec.Command("age-keygen", "-o", identityPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("age-keygen failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if err := os.Chmod(identityPath, 0o600); err != nil {
		return "", fmt.Errorf("chmod identity key: %w", err)
	}
	pub, err := RecipientFromIdentity(identityPath)
	if err != nil {
		return "", err
	}
	return pub, nil
}

func RecipientFromIdentity(identityPath string) (string, error) {
	f, err := os.Open(identityPath)
	if err != nil {
		return "", fmt.Errorf("open identity key: %w", err)
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if strings.HasPrefix(line, "# public key:") {
			pub := strings.TrimSpace(strings.TrimPrefix(line, "# public key:"))
			if pub == "" {
				continue
			}
			return pub, nil
		}
	}
	if err := s.Err(); err != nil {
		return "", fmt.Errorf("read identity key: %w", err)
	}
	return "", fmt.Errorf("public key not found in identity key")
}

func EncryptFile(inputTarPath, outputBundlePath, recipient string) error {
	if err := EnsureAgeInstalled(); err != nil {
		return err
	}
	if recipient == "" {
		return fmt.Errorf("recipient is required")
	}
	cmd := exec.Command("age", "-r", recipient, "-o", outputBundlePath, inputTarPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("age encrypt failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func DecryptFile(inputBundlePath, identityKeyPath, outputTarPath string) error {
	if err := EnsureAgeInstalled(); err != nil {
		return err
	}
	cmd := exec.Command("age", "-d", "-i", identityKeyPath, "-o", outputTarPath, inputBundlePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("age decrypt failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
