package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/spf13/cobra"

	"github.com/Azimuthal-HQ/azimuthal/internal/config"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/storage"
)

var restoreInput string

var restoreCmd = &cobra.Command{
	Use:   "restore",
	Short: "Restore from a backup archive",
	Long: `Restores Azimuthal from a .tar.gz backup created by "azimuthal backup".

The restore process:
  1. Validates the manifest before doing anything
  2. Restores the PostgreSQL database via psql
  3. Restores all object storage files
  4. Is idempotent — safe to run twice without corruption`,
	RunE: runRestore,
}

func init() {
	restoreCmd.Flags().StringVar(&restoreInput, "input", "", "input backup file path (required)")
	_ = restoreCmd.MarkFlagRequired("input")
}

// runRestore reads a backup archive and restores the database and object storage.
//
// # Why every RunE in this binary starts by silencing usage
//
// cobra prints the command's full usage block after any error a RunE returns.
// For this command that buried "the database is in an indeterminate state"
// under forty lines of flag documentation, at the one moment an operator most
// needs to read it — and docs/self-hosting.md now tells them to read it.
//
// It is set HERE, inside the RunE, rather than as a SilenceUsage field on the
// command or on the root, and the difference is not stylistic. Flag parsing
// happens before RunE runs, so a mistyped `--inptu` still gets the usage block,
// which is exactly when usage helps; only failures from the command's own body
// suppress it. The field form cannot make that distinction — it silences both.
//
// Applied uniformly to all eight RunEs, including `assess` and `bundle-hash`,
// which previously used the field form and so behaved differently from the
// other six. Two guards keep it that way:
// TestCommands_EveryRunESilencesUsage walks the AST so a ninth command cannot
// forget, and TestCommands_SilenceUsageOnRuntimeFailure asserts the behaviour
// through cobra in both directions.
func runRestore(cmd *cobra.Command, _ []string) error {
	cmd.SilenceUsage = true // runtime failure, not a usage error — see TestCommands_SilenceUsageOnRuntimeFailure
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	entries, err := readArchive(restoreInput)
	if err != nil {
		return err
	}

	manifest, err := validateManifest(entries)
	if err != nil {
		return err
	}

	// Refuse to restore while a server is live. A restore replays a
	// --clean --if-exists dump — it DROPs and recreates every table — and then
	// overwrites object storage, so running it against a database a serve
	// process is actively writing (the River queue, request handlers) corrupts
	// both. The advisory lock is the enforcement of the "stop the app first"
	// step docs/self-hosting.md and docs/upgrade.md now spell out; until it
	// existed the invariant was documentation only, and our own docs violated it
	// by telling operators to run this inside the live app container.
	//
	// Taken here — after the archive has been read and validated (both touch
	// only the archive file, never the database) and immediately before the
	// first step that MUTATES the datastore — so a held lock leaves the database
	// and object storage untouched, and an invalid archive is still reported
	// without needing a database at all. Held through restoreStorage too, via
	// defer. See cmd/server/dblock.go.
	lock, err := restoreTryStoreLock(context.Background(), cfg.DatabaseURL)
	if err != nil {
		return err // errServerRunning carries the operator instruction verbatim
	}
	defer lock.Release()

	if err := restoreDatabase(cfg, entries); err != nil {
		return err
	}

	if err := restoreStorage(cfg, entries); err != nil {
		return err
	}

	fmt.Printf("Restore complete (%d files in manifest).\n", len(manifest.Files))
	return nil
}

// readArchive opens and decompresses a .tar.gz backup, returning all entries.
func readArchive(path string) (map[string][]byte, error) {
	inFile, err := os.Open(path) // #nosec G304 -- user-provided CLI flag
	if err != nil {
		return nil, fmt.Errorf("opening backup file: %w", err)
	}
	defer func() { _ = inFile.Close() }()

	gr, err := gzip.NewReader(inFile)
	if err != nil {
		return nil, fmt.Errorf("decompressing backup: %w", err)
	}
	defer func() { _ = gr.Close() }()

	tr := tar.NewReader(gr)
	entries := make(map[string][]byte)

	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading archive entry: %w", err)
		}

		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("reading archive data for %s: %w", header.Name, err)
		}
		entries[header.Name] = data
	}

	return entries, nil
}

// validateManifest checks the manifest.json is present and all referenced files exist.
func validateManifest(entries map[string][]byte) (*backupManifest, error) {
	fmt.Println("Validating backup manifest...")
	manifestData, ok := entries["manifest.json"]
	if !ok {
		return nil, fmt.Errorf("invalid backup: manifest.json not found")
	}

	var manifest backupManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return nil, fmt.Errorf("invalid manifest: %w", err)
	}

	fmt.Printf("  Azimuthal version: %s\n", manifest.AzimuthalVersion)
	fmt.Printf("  Backup timestamp:  %s\n", manifest.BackupTimestamp.Format("2006-01-02 15:04:05 UTC"))
	// Provenance the operator needs before restoring: a dump taken from a
	// newer server than the one being restored into will fail part-way.
	// Nothing read this field back before, which made recording it theatre.
	if manifest.PostgresVersion != "" {
		fmt.Printf("  Source postgres:   %s\n", manifest.PostgresVersion)
	}
	fmt.Printf("  Files in archive:  %d\n", len(manifest.Files))

	if err := verifyManifestedFiles(&manifest, entries); err != nil {
		return nil, err
	}

	fmt.Println("  Manifest valid.")
	return &manifest, nil
}

// verifyManifestedFiles checks that every file the manifest lists is present in
// the archive and, when the manifest carries digests, that each member's bytes
// still hash to what the backup recorded.
//
// # Present digests are verified; their absence is a warning, not a refusal
//
// An archive written before per-file digests existed carries no FileDigests at
// all. Refusing it would strand every backup the operator already holds — "no
// users" is not "no old archives" — so the empty case restores with a printed
// warning and no integrity check, exactly the data it would have restored
// before this field was added. An archive that DOES carry digests is held to
// them: a truncated or altered member is caught here, named, and refused before
// restore touches the database or object store.
//
// When digests are present they must be COMPLETE. A manifest that lists a file
// but omits its digest is not a legacy archive — it is a new-style one with a
// hole, which is the exact shape a tamperer produces by stripping the digest of
// the member they rewrote. That is a refusal, not a silent skip.
func verifyManifestedFiles(manifest *backupManifest, entries map[string][]byte) error {
	haveDigests := len(manifest.FileDigests) > 0

	for _, f := range manifest.Files {
		data, exists := entries[f]
		if !exists {
			return fmt.Errorf("invalid backup: manifest references %q but file not in archive", f)
		}
		if !haveDigests {
			continue
		}
		want, ok := manifest.FileDigests[f]
		if !ok {
			return fmt.Errorf("invalid backup: manifest carries digests but none for %q — the "+
				"manifest is inconsistent with itself, which is what a stripped digest looks like; "+
				"refusing to restore", f)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != want {
			return fmt.Errorf("invalid backup: %q fails its integrity check — the manifest records "+
				"sha256 %s but the archive contains %s. The archive is truncated or tampered; "+
				"refusing to restore", f, want, got)
		}
	}

	if haveDigests {
		fmt.Printf("  Verified SHA-256 for %d files.\n", len(manifest.Files))
	} else {
		fmt.Println("  Warning: this archive predates per-file digests — restoring without integrity verification.")
	}
	return nil
}

// errNoDatabaseDump is returned when an archive carries no database.sql.
//
// There is no storage-only backup mode to accommodate: runBackup dumps
// postgres as its first step, unconditionally, and returns an error if the
// dump fails — every archive this tool produces contains a database.sql. So an
// archive without one is not a partial backup, it is a corrupt or foreign one,
// and the only honest thing to do with it is refuse.
var errNoDatabaseDump = errors.New("invalid backup: archive contains no database.sql")

// restoreDatabase restores the PostgreSQL dump from the archive.
//
// A missing dump is a failure, not a skip. This printed "No database dump
// found in backup, skipping." and returned nil, so an archive that had never
// captured a dump ran to "Restore complete" having restored nothing — the same
// shape of defect as D105's partial restore and with the same consequence: the
// operator believes the database is back. validateManifest cannot catch it
// either, because it only checks that files the manifest *lists* are present,
// and a manifest that lists no dump passes.
func restoreDatabase(cfg *config.Config, entries map[string][]byte) error {
	dbDump, exists := entries["database.sql"]
	if !exists {
		return errNoDatabaseDump
	}

	fmt.Println("Restoring PostgreSQL database...")
	if err := restorePostgres(cfg.DatabaseURL, dbDump); err != nil {
		return fmt.Errorf("restoring postgres: %w", err)
	}
	fmt.Println("  Database restored.")
	return nil
}

// restoreStorage restores object storage files from the archive if configured.
func restoreStorage(cfg *config.Config, entries map[string][]byte) error {
	if cfg.StorageEndpoint == "" {
		fmt.Println("Skipping object storage (no STORAGE_ENDPOINT configured).")
		return nil
	}

	fmt.Println("Restoring object storage...")
	count, err := restoreObjectStorage(cfg, entries)
	if err != nil {
		return fmt.Errorf("restoring object storage: %w", err)
	}
	fmt.Printf("  Restored %d files to object storage.\n", count)
	return nil
}

// restorePostgres runs the SQL dump through psql to restore the database.
// Uses --clean and --if-exists in the dump, making this idempotent.
//
// -v ON_ERROR_STOP=1 is load-bearing and must not be removed. Without it psql
// reports the exit status of its *last* statement and keeps going after a
// failure, so a dump whose statements all failed still exits 0 — and this
// function returned nil, and restoreDatabase printed "  Database restored."
// over an empty database. An operator only discovers that on the day they are
// recovering from an incident. A partial restore is a failure, not a success.
//
// psql's diagnostics are captured rather than streamed so they can be attached
// to the returned error: the caller aborts the whole restore on error, and
// "exit status 3" on its own does not say which statement failed. Stdout is
// captured for the same reason — it used to go to io.Discard, which threw away
// the NOTICE/ERROR context that says what went wrong.
func restorePostgres(databaseURL string, dump []byte) error {
	cmd := exec.Command("psql", "-v", "ON_ERROR_STOP=1", databaseURL) // #nosec G204,G702 -- trusted config value
	cmd.Stdin = bytes.NewReader(dump)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		if detail == "" {
			return fmt.Errorf("psql restore failed: %w", err)
		}
		return fmt.Errorf("psql restore failed: %w: %s", err, detail)
	}
	return nil
}

// restoreObjectStorage uploads all storage/* entries back to the configured bucket.
// Uses PutObject which overwrites existing keys, making this idempotent.
func restoreObjectStorage(cfg *config.Config, entries map[string][]byte) (int, error) {
	endpoint, useSSL := storage.NormalizeEndpoint(cfg.StorageEndpoint, cfg.StorageUseSSL)
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.StorageAccessKey, cfg.StorageSecretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return 0, fmt.Errorf("connecting to object storage: %w", err)
	}

	ctx := context.Background()

	// Ensure bucket exists
	exists, err := client.BucketExists(ctx, cfg.StorageBucket)
	if err != nil {
		return 0, fmt.Errorf("checking bucket: %w", err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, cfg.StorageBucket, minio.MakeBucketOptions{}); err != nil {
			return 0, fmt.Errorf("creating bucket: %w", err)
		}
	}

	count := 0
	for name, data := range entries {
		if !strings.HasPrefix(name, "storage/") {
			continue
		}

		key := stripStoragePrefix(name)
		reader := bytes.NewReader(data)

		_, err := client.PutObject(ctx, cfg.StorageBucket, key, reader, int64(len(data)), minio.PutObjectOptions{})
		if err != nil {
			return count, fmt.Errorf("uploading %s: %w", key, err)
		}
		count++
	}

	return count, nil
}
