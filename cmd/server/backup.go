package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/spf13/cobra"

	"github.com/Azimuthal-HQ/azimuthal/internal/config"
)

var backupOutput string

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Create a full backup (database + object storage)",
	Long: `Creates a compressed .tar.gz archive containing:
  - A PostgreSQL dump (pg_dump)
  - All object storage files from MinIO/S3
  - A manifest.json with version, timestamp, and file list`,
	RunE: runBackup,
}

func init() {
	backupCmd.Flags().StringVar(&backupOutput, "output", "", "output file path (required)")
	_ = backupCmd.MarkFlagRequired("output")
}

// backupManifest describes the contents of a backup archive.
type backupManifest struct {
	AzimuthalVersion string    `json:"azimuthal_version"`
	BackupTimestamp  time.Time `json:"backup_timestamp"`
	PostgresVersion  string    `json:"postgres_version,omitempty"`
	Files            []string  `json:"files"`
}

// runBackup creates a full backup archive at the path specified by --output.
func runBackup(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	outFile, err := os.Create(backupOutput) // #nosec G304 -- user-provided CLI flag
	if err != nil {
		return fmt.Errorf("creating output file: %w", err)
	}
	defer func() { _ = outFile.Close() }()

	gw := gzip.NewWriter(outFile)
	defer func() { _ = gw.Close() }()

	tw := tar.NewWriter(gw)
	defer func() { _ = tw.Close() }()

	manifest := backupManifest{
		AzimuthalVersion: Version,
		BackupTimestamp:  time.Now().UTC(),
	}

	// Step 1: PostgreSQL dump
	fmt.Println("Backing up PostgreSQL database...")
	pgDump, pgVersion, err := dumpPostgres(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("dumping postgres: %w", err)
	}
	manifest.PostgresVersion = pgVersion

	if err := addToTar(tw, "database.sql", pgDump); err != nil {
		return fmt.Errorf("writing database dump to archive: %w", err)
	}
	manifest.Files = append(manifest.Files, "database.sql")
	fmt.Println("  Database dump complete.")

	// Step 2: Object storage files
	if cfg.StorageEndpoint != "" {
		fmt.Println("Backing up object storage...")
		files, err := backupObjectStorage(tw, cfg, &manifest)
		if err != nil {
			return fmt.Errorf("backing up object storage: %w", err)
		}
		fmt.Printf("  Backed up %d files from object storage.\n", files)
	} else {
		fmt.Println("Skipping object storage (no STORAGE_ENDPOINT configured).")
	}

	// Step 3: Write manifest
	fmt.Println("Writing manifest...")
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling manifest: %w", err)
	}
	if err := addToTar(tw, "manifest.json", manifestJSON); err != nil {
		return fmt.Errorf("writing manifest to archive: %w", err)
	}

	fmt.Printf("Backup complete: %s\n", backupOutput)
	return nil
}

// dumpPostgres runs pg_dump and returns the SQL dump bytes and the postgres version.
func dumpPostgres(databaseURL string) ([]byte, string, error) {
	// Get postgres version
	versionCmd := exec.Command("psql", databaseURL, "-t", "-c", "SELECT version();") // #nosec G204,G702 -- trusted config value
	versionOut, _ := versionCmd.Output()
	pgVersion := string(versionOut)

	// Run pg_dump
	cmd := exec.Command("pg_dump", "--no-owner", "--no-acl", "--clean", "--if-exists", databaseURL) // #nosec G204,G702 -- trusted config value
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, "", fmt.Errorf("pg_dump failed: %s", string(exitErr.Stderr))
		}
		return nil, "", fmt.Errorf("pg_dump failed: %w", err)
	}

	return out, pgVersion, nil
}

// backupObjectStorage copies all objects from the configured bucket into the tar archive.
func backupObjectStorage(tw *tar.Writer, cfg *config.Config, manifest *backupManifest) (int, error) {
	client, err := minio.New(cfg.StorageEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.StorageAccessKey, cfg.StorageSecretKey, ""),
		Secure: cfg.StorageUseSSL,
	})
	if err != nil {
		return 0, fmt.Errorf("connecting to object storage: %w", err)
	}

	ctx := context.Background()
	count := 0

	for obj := range client.ListObjects(ctx, cfg.StorageBucket, minio.ListObjectsOptions{Recursive: true}) {
		if obj.Err != nil {
			return count, fmt.Errorf("listing objects: %w", obj.Err)
		}

		reader, err := client.GetObject(ctx, cfg.StorageBucket, obj.Key, minio.GetObjectOptions{})
		if err != nil {
			return count, fmt.Errorf("getting object %s: %w", obj.Key, err)
		}

		data, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			return count, fmt.Errorf("reading object %s: %w", obj.Key, err)
		}

		archivePath := "storage/" + obj.Key
		if err := addToTar(tw, archivePath, data); err != nil {
			return count, fmt.Errorf("writing object %s to archive: %w", obj.Key, err)
		}

		manifest.Files = append(manifest.Files, archivePath)
		count++
	}

	return count, nil
}

// addToTar writes a single file entry to the tar archive.
func addToTar(tw *tar.Writer, name string, data []byte) error {
	header := &tar.Header{
		Name:    name,
		Size:    int64(len(data)),
		Mode:    0644,
		ModTime: time.Now().UTC(),
	}
	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("writing tar header for %s: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("writing tar data for %s: %w", name, err)
	}
	return nil
}

// stripStoragePrefix removes "storage/" prefix from an archive path
// to get the original object key.
func stripStoragePrefix(archivePath string) string {
	const prefix = "storage/"
	if len(archivePath) > len(prefix) && archivePath[:len(prefix)] == prefix {
		return archivePath[len(prefix):]
	}
	return archivePath
}
