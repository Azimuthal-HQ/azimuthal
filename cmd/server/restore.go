package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
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
func runRestore(_ *cobra.Command, _ []string) error {
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

	for _, f := range manifest.Files {
		if _, exists := entries[f]; !exists {
			return nil, fmt.Errorf("invalid backup: manifest references %q but file not in archive", f)
		}
	}
	fmt.Println("  Manifest valid.")
	return &manifest, nil
}

// restoreDatabase restores the PostgreSQL dump from the archive if present.
func restoreDatabase(cfg *config.Config, entries map[string][]byte) error {
	dbDump, exists := entries["database.sql"]
	if !exists {
		fmt.Println("No database dump found in backup, skipping.")
		return nil
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
