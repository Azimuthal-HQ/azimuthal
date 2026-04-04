package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/spf13/cobra"

	"github.com/Azimuthal-HQ/azimuthal/internal/config"
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
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	inFile, err := os.Open(restoreInput)
	if err != nil {
		return fmt.Errorf("opening backup file: %w", err)
	}
	defer inFile.Close()

	gr, err := gzip.NewReader(inFile)
	if err != nil {
		return fmt.Errorf("decompressing backup: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	// First pass: read all entries into memory for manifest validation
	entries := make(map[string][]byte)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading archive entry: %w", err)
		}

		data, err := io.ReadAll(tr)
		if err != nil {
			return fmt.Errorf("reading archive data for %s: %w", header.Name, err)
		}
		entries[header.Name] = data
	}

	// Validate manifest
	fmt.Println("Validating backup manifest...")
	manifestData, ok := entries["manifest.json"]
	if !ok {
		return fmt.Errorf("invalid backup: manifest.json not found")
	}

	var manifest backupManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return fmt.Errorf("invalid manifest: %w", err)
	}

	fmt.Printf("  Azimuthal version: %s\n", manifest.AzimuthalVersion)
	fmt.Printf("  Backup timestamp:  %s\n", manifest.BackupTimestamp.Format("2006-01-02 15:04:05 UTC"))
	fmt.Printf("  Files in archive:  %d\n", len(manifest.Files))

	// Verify all manifest files exist in archive
	for _, f := range manifest.Files {
		if _, exists := entries[f]; !exists {
			return fmt.Errorf("invalid backup: manifest references %q but file not in archive", f)
		}
	}
	fmt.Println("  Manifest valid.")

	// Step 1: Restore database
	if dbDump, exists := entries["database.sql"]; exists {
		fmt.Println("Restoring PostgreSQL database...")
		if err := restorePostgres(cfg.DatabaseURL, dbDump); err != nil {
			return fmt.Errorf("restoring postgres: %w", err)
		}
		fmt.Println("  Database restored.")
	} else {
		fmt.Println("No database dump found in backup, skipping.")
	}

	// Step 2: Restore object storage
	if cfg.StorageEndpoint != "" {
		fmt.Println("Restoring object storage...")
		count, err := restoreObjectStorage(cfg, entries)
		if err != nil {
			return fmt.Errorf("restoring object storage: %w", err)
		}
		fmt.Printf("  Restored %d files to object storage.\n", count)
	} else {
		fmt.Println("Skipping object storage (no STORAGE_ENDPOINT configured).")
	}

	fmt.Println("Restore complete.")
	return nil
}

// restorePostgres runs the SQL dump through psql to restore the database.
// Uses --clean and --if-exists in the dump, making this idempotent.
func restorePostgres(databaseURL string, dump []byte) error {
	cmd := exec.Command("psql", databaseURL)
	cmd.Stdin = bytes.NewReader(dump)
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("psql restore failed: %w", err)
	}
	return nil
}

// restoreObjectStorage uploads all storage/* entries back to the configured bucket.
// Uses PutObject which overwrites existing keys, making this idempotent.
func restoreObjectStorage(cfg *config.Config, entries map[string][]byte) (int, error) {
	client, err := minio.New(cfg.StorageEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.StorageAccessKey, cfg.StorageSecretKey, ""),
		Secure: cfg.StorageUseSSL,
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
