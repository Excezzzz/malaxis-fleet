package backup

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"malaxis-fleet/internal/config"
)

type Engine struct {
	cfg       *config.Config
	backupDir string
}

func NewEngine(cfg *config.Config) *Engine {
	return &Engine{
		cfg:       cfg,
		backupDir: "backups",
	}
}

// CreateBackup creates a new backup of the PostgreSQL database using pg_dump.
func (e *Engine) CreateBackup() (string, error) {
	if err := os.MkdirAll(e.backupDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create backup directory: %w", err)
	}

	timestamp := time.Now().Format("2006-01-02_1504")
	dumpFileName := fmt.Sprintf("dump_%s.sql", timestamp)
	dumpFilePath := filepath.Join(os.TempDir(), dumpFileName)

	// The pg_dump command
	cmd := exec.Command("pg_dump",
		"-U", e.cfg.PostgresUser,
		"-h", e.cfg.PostgresHost,
		"-p", e.cfg.PostgresPort,
		"-d", e.cfg.PostgresDB,
		"-f", dumpFilePath,
		"--clean", // Optional: Add 'DROP TABLE' statements
	)

	// Set the password via environment variable to avoid it appearing in process lists
	cmd.Env = append(os.Environ(), fmt.Sprintf("PGPASSWORD=%s", e.cfg.PostgresPassword))

	// Run pg_dump
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("pg_dump failed: %w\nOutput: %s", err, string(output))
	}

	// Ensure the temporary dump file is cleaned up
	defer os.Remove(dumpFilePath)

	// Create the final zip archive
	zipFileName := fmt.Sprintf("malaxis_fleet_backup_%s.zip", timestamp)
	zipFilePath := filepath.Join(e.backupDir, zipFileName)
	zipFile, err := os.Create(zipFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to create zip file: %w", err)
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	// Add the SQL dump to the zip file
	if err := e.addFileToZip(zipWriter, dumpFilePath); err != nil {
		os.Remove(zipFilePath) // Clean up partially created zip file
		return "", fmt.Errorf("failed to add dump to zip: %w", err)
	}

	// After a successful backup, enforce retention policy
	if err := e.enforceRetention(); err != nil {
		fmt.Printf("Warning: failed to enforce backup retention policy: %v\n", err)
	}

	return zipFilePath, nil
}

// GetLatestBackup returns the path to the most recent backup file.
func (e *Engine) GetLatestBackup() (string, error) {
	files, err := e.getBackupFiles()
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no backups found")
	}
	return files[len(files)-1], nil
}

// addFileToZip is a helper to add a single file to a zip archive.
func (e *Engine) addFileToZip(zipWriter *zip.Writer, filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return err
	}

	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = filepath.Base(filePath)
	header.Method = zip.Deflate // Use compression

	writer, err := zipWriter.CreateHeader(header)
	if err != nil {
		return err
	}

	_, err = io.Copy(writer, file)
	return err
}

// getBackupFiles retrieves a sorted list of backup file paths.
func (e *Engine) getBackupFiles() ([]string, error) {
	var files []string
	err := filepath.Walk(e.backupDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasPrefix(info.Name(), "malaxis_fleet_backup_") && strings.HasSuffix(info.Name(), ".zip") {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files) // Sorts from oldest to newest
	return files, err
}

// enforceRetention ensures that the number of backups does not exceed the configured maximum.
func (e *Engine) enforceRetention() error {
	files, err := e.getBackupFiles()
	if err != nil {
		return err
	}

	if len(files) <= e.cfg.MaxBackupRetention {
		return nil
	}

	filesToDelete := files[:len(files)-e.cfg.MaxBackupRetention]
	for _, file := range filesToDelete {
		fmt.Printf("Deleting old backup: %s\n", file)
		if err := os.Remove(file); err != nil {
			fmt.Printf("Warning: failed to delete old backup %s: %v\n", file, err)
		}
	}

	return nil
}
