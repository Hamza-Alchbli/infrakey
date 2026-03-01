package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"infrakey/internal/bundle"
	"infrakey/internal/restore"
)

func main() {
	if len(os.Args) < 2 {
		printUsage(os.Stderr)
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "snapshot":
		err = runSnapshot(os.Args[2:])
	case "restore":
		err = runRestore(os.Args[2:])
	case "inspect":
		err = runInspect(os.Args[2:])
	case "-h", "--help", "help":
		printUsage(os.Stdout)
		return
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func runSnapshot(args []string) error {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	root := fs.String("root", ".", "Root directory to scan for compose files")
	out := fs.String("out", "vault.bundle", "Output encrypted bundle path")
	recipient := fs.String("recipient", "", "Age recipient public key")
	identityOut := fs.String("identity-out", "identity.key", "Identity key output path (used when --recipient is not set)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}

	summary, err := bundle.CreateSnapshot(bundle.SnapshotOptions{
		RootDir:     *root,
		OutBundle:   *out,
		Recipient:   *recipient,
		IdentityOut: *identityOut,
	})
	if err != nil {
		return err
	}

	outAbs, _ := filepath.Abs(*out)
	fmt.Printf("Snapshot complete\n")
	fmt.Printf("- Bundle: %s\n", outAbs)
	if summary.GeneratedIdentityPath != "" {
		fmt.Printf("- Identity key: %s\n", summary.GeneratedIdentityPath)
	}
	fmt.Printf("- Compose files: %d\n", summary.ComposeFiles)
	fmt.Printf("- Captured files: %d\n", summary.CapturedFiles)
	fmt.Printf("- External files: %d\n", summary.ExternalFiles)
	if summary.SkippedMissing > 0 {
		fmt.Printf("- Skipped missing references: %d\n", summary.SkippedMissing)
	}
	return nil
}

func runRestore(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	bundlePath := fs.String("bundle", "", "Encrypted bundle path")
	identityKey := fs.String("identity-key", "", "Identity key path")
	target := fs.String("target", "", "Restore target directory")
	yes := fs.Bool("yes", false, "Non-interactive mode")
	includeExternal := fs.String("include-external", "", "External file policy: all|none")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *bundlePath == "" || *identityKey == "" || *target == "" {
		return fmt.Errorf("--bundle, --identity-key and --target are required")
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}

	summary, err := restore.Run(restore.Options{
		BundlePath:      *bundlePath,
		IdentityKeyPath: *identityKey,
		TargetDir:       *target,
		Yes:             *yes,
		IncludeExternal: *includeExternal,
	})
	if err != nil {
		return err
	}

	targetAbs, _ := filepath.Abs(*target)
	fmt.Printf("Restore complete\n")
	fmt.Printf("- Target: %s\n", targetAbs)
	fmt.Printf("- Restored entries: %d\n", summary.RestoredEntries)
	fmt.Printf("- External references total: %d\n", summary.ExternalTotal)
	fmt.Printf("- External references skipped: %d\n", summary.SkippedExternal)
	return nil
}

func runInspect(args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	bundlePath := fs.String("bundle", "", "Encrypted bundle path")
	identityKey := fs.String("identity-key", "", "Identity key path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *bundlePath == "" || *identityKey == "" {
		return fmt.Errorf("--bundle and --identity-key are required")
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}

	res, err := bundle.Inspect(bundle.InspectOptions{
		BundlePath:      *bundlePath,
		IdentityKeyPath: *identityKey,
	})
	if err != nil {
		return err
	}

	fmt.Printf("Bundle metadata\n")
	fmt.Printf("- pciVersion: %s\n", res.Manifest.PCIVersion)
	fmt.Printf("- snapshotId: %s\n", res.Manifest.SnapshotID)
	fmt.Printf("- createdAt: %s\n", res.Manifest.CreatedAt)
	fmt.Printf("- sourceRoot: %s\n", res.Manifest.SourceRoot)
	fmt.Printf("- entries: %d\n", len(res.Entries))
	fmt.Printf("- external entries: %d\n", len(res.External))

	fmt.Printf("\nPlanned restore tree\n")
	for _, e := range res.Entries {
		fmt.Printf("- <target>/%s (%s)\n", e.RestoreRelPath, e.Kind)
	}

	if len(res.External) > 0 {
		fmt.Printf("\nExternal references\n")
		for _, e := range res.External {
			fmt.Printf("- %s\n", e.SourceAbsPath)
		}
	}
	return nil
}

func printUsage(w *os.File) {
	fmt.Fprintln(w, "InfraKey (Linux-only MVP)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  infrakey snapshot --root <dir> --out <vault.bundle> [--recipient <age-pubkey>] [--identity-out <identity.key>]")
	fmt.Fprintln(w, "  infrakey restore --bundle <vault.bundle> --identity-key <identity.key> --target <dir> [--yes] [--include-external all|none]")
	fmt.Fprintln(w, "  infrakey inspect --bundle <vault.bundle> --identity-key <identity.key>")
}
