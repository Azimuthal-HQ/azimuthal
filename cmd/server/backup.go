package main

import (
	"archive/tar"
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
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/spf13/cobra"

	"github.com/Azimuthal-HQ/azimuthal/internal/config"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/storage"
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

// backupArchiveMode is the permission the archive file is created with.
//
// This was os.Create, which is 0666-before-umask — 0644 on any host with the
// default umask of 022, i.e. world-readable. The archive is the densest secret
// this system produces: a full pg_dump (every password hash, every session and
// portal token, every private page and ticket) plus every byte in object
// storage, in one file that operators leave sitting in /var/backups and copy
// around. Owner-only, and the tar entries' own 0644 modes are left alone —
// those govern what a restore-to-another-system unpacks, not what is readable
// on the machine the backup was taken on.
const backupArchiveMode os.FileMode = 0o600

// openBackupOutput creates the archive's destination file.
//
// It is a variable rather than a direct call so that
// TestRunBackup_FlushFailureIsNotASuccess can substitute a sink whose Close
// fails. The defect that test guards is unreachable otherwise: a truncated
// archive is the product of writes that all *succeed* followed by a flush that
// does not, so provoking it deterministically means controlling the sink.
// Production has exactly one implementation, and it is this one.
var openBackupOutput = func(path string) (io.WriteCloser, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, backupArchiveMode) // #nosec G304 -- user-provided CLI flag
	if err != nil {
		return nil, fmt.Errorf("creating output file: %w", err)
	}
	// Returned as a named value rather than forwarding os.OpenFile's results
	// directly: on error that would hand back a typed-nil *os.File inside a
	// non-nil io.WriteCloser, and the caller's `!= nil` check would pass.
	return f, nil
}

// backupManifest describes the contents of a backup archive.
//
// FileDigests carries a hex SHA-256 per archived member, keyed by the same name
// that appears in Files. It is what lets restore prove a member was neither
// truncated nor altered between backup and recovery — the manifest lists what
// the archive should contain, and the digest says the bytes are still those.
//
// It is a SEPARATE, `omitempty` map rather than a change to Files' element type
// on purpose: an archive taken before this field existed marshals `"files"` as
// a plain string array, and widening Files to a struct would refuse to
// unmarshal every such archive. The maintainer keeps his own; "no users" is not
// "no old archives". So new archives populate both, old archives carry only
// Files, and restore reads the presence of FileDigests to decide whether it can
// verify — see validateManifest.
type backupManifest struct {
	AzimuthalVersion string            `json:"azimuthal_version"`
	BackupTimestamp  time.Time         `json:"backup_timestamp"`
	PostgresVersion  string            `json:"postgres_version,omitempty"`
	Files            []string          `json:"files"`
	FileDigests      map[string]string `json:"file_digests,omitempty"`
}

// recordFile registers one archived member in the manifest: it appends the name
// to Files and stores the member's SHA-256 in FileDigests under the same key.
//
// Callers invoke it only AFTER addToTar has succeeded, so a member that failed
// to write is never announced in the manifest as present. manifest.json is
// itself never recorded here — it cannot carry its own digest, and it is the
// document restore validates the others against, not one of them.
func (m *backupManifest) recordFile(name string, data []byte) {
	m.Files = append(m.Files, name)
	if m.FileDigests == nil {
		m.FileDigests = make(map[string]string)
	}
	sum := sha256.Sum256(data)
	m.FileDigests[name] = hex.EncodeToString(sum[:])
}

// runBackup creates a full backup archive at the path specified by --output.
func runBackup(cmd *cobra.Command, _ []string) error {
	cmd.SilenceUsage = true // runtime failure, not a usage error — see TestCommands_SilenceUsageOnRuntimeFailure
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	outFile, err := openBackupOutput(backupOutput)
	if err != nil {
		return err
	}

	// These three defers are cleanup for the error paths below, which return
	// without reaching finalizeArchive — they release the descriptor and
	// nothing more. Their errors are discarded HERE, and only here, because
	// the path that matters — the one that prints success — goes through
	// finalizeArchive, which closes all three and checks every error.
	//
	// Closing twice is defined and harmless: tar.Writer and gzip.Writer both
	// return nil once already closed, and os.File returns ErrClosed, which is
	// exactly what these discard.
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
	manifest.recordFile("database.sql", pgDump)
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
	if err := writeManifest(tw, &manifest); err != nil {
		return err
	}

	// Step 4: flush. Everything above is written; none of it is necessarily on
	// disk. The tar footer, the gzip trailer and the file's own buffers all
	// land here, and a failure in any of them leaves a truncated archive that
	// restore cannot read.
	//
	// This used to be three deferred closes with their errors discarded, which
	// run AFTER the success line has printed and AFTER the function has
	// returned nil — so a backup that failed to flush reported success and the
	// operator found out while restoring. "Backup complete" now prints only
	// once every byte is flushed and every close has succeeded.
	if err := finalizeArchive(tw, gw, outFile); err != nil {
		return err
	}

	fmt.Printf("Backup complete: %s\n", backupOutput)
	return nil
}

// writeManifest marshals the manifest and adds it to the archive as its final
// entry. Extracted from runBackup, which the flush step pushed one statement
// past the funlen limit; this is the step that most obviously stands alone.
func writeManifest(tw *tar.Writer, manifest *backupManifest) error {
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling manifest: %w", err)
	}
	if err := addToTar(tw, "manifest.json", manifestJSON); err != nil {
		return fmt.Errorf("writing manifest to archive: %w", err)
	}
	return nil
}

// finalizeArchive closes the archive's three nested writers, innermost first,
// and returns the first failure.
//
// The order is load-bearing, not stylistic: tw.Close writes the tar footer
// *into* gw, and gw.Close writes the gzip trailer *into* out. Closing out
// first truncates both; closing gw before tw loses the footer. Each error is
// checked separately because each one means a different unreadable archive,
// and because the caller must not print success after any of them.
//
// It short-circuits rather than closing the rest: once a layer has failed the
// archive is already corrupt, and runBackup's deferred closes still release
// every descriptor.
//
// Takes io.Closer rather than the concrete writer types so the ordering and
// the error handling can be asserted directly, without a postgres server —
// see TestFinalizeArchive_ClosesInOrderAndReportsEveryFailure.
func finalizeArchive(tarWriter, gzipWriter, outFile io.Closer) error {
	if err := tarWriter.Close(); err != nil {
		return fmt.Errorf("finalising tar archive: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return fmt.Errorf("finalising gzip stream: %w", err)
	}
	if err := outFile.Close(); err != nil {
		return fmt.Errorf("closing backup file: %w", err)
	}
	return nil
}

// dumpPostgres runs pg_dump and returns the SQL dump bytes and the postgres version.
//
// The version probe's error is returned rather than discarded. It used to be
// `versionOut, _ :=`, so a missing or failing psql wrote an empty
// PostgresVersion into the manifest and said nothing — and since restore reads
// that field back to tell the operator which server the dump came from, an
// empty one silently removes the only provenance the archive carries. Failing
// here is also the honest answer for a second reason: restore forks psql, so a
// backup taken where psql is unavailable is a backup that cannot be restored
// by this tool. Better to learn that at 2 AM on the cron than during recovery.
func dumpPostgres(databaseURL string) ([]byte, string, error) {
	// Get postgres version. -t drops the header, -A the column padding.
	versionCmd := exec.Command("psql", "-v", "ON_ERROR_STOP=1", "-t", "-A", "-c", "SELECT version();", databaseURL) // #nosec G204,G702 -- trusted config value
	versionOut, err := versionCmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, "", fmt.Errorf("probing postgres version: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, "", fmt.Errorf("probing postgres version: %w", err)
	}
	pgVersion := strings.TrimSpace(string(versionOut))

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
	endpoint, useSSL := storage.NormalizeEndpoint(cfg.StorageEndpoint, cfg.StorageUseSSL)
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.StorageAccessKey, cfg.StorageSecretKey, ""),
		Secure: useSSL,
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

		manifest.recordFile(archivePath, data)
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
