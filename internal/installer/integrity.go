package installer

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func fileSHA256(path string, limit int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, limit+1))
	if err != nil {
		return "", err
	}
	if n > limit {
		return "", fmt.Errorf("archivo excede límite: %s", path)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// Digest names, file content and link targets in lexical order. Timestamps,
// ownership and umask are deliberately excluded from reproducible runtime pins.
func directorySHA256(root string) (string, error) {
	if err := checkPath(root); err != nil {
		return "", err
	}
	hash := sha256.New()
	count := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		count++
		if count > 30000 {
			return fmt.Errorf("demasiados archivos en runtime")
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		kind, digest, err := runtimeEntryDigest(path, entry)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00%s\n", filepath.ToSlash(rel), kind, digest)
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func runtimeEntryDigest(path string, entry fs.DirEntry) (string, string, error) {
	if entry.Type()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		return "link", target, err
	}
	if !entry.Type().IsRegular() {
		return "", "", fmt.Errorf("archivo especial en runtime: %s", path)
	}
	digest, err := fileSHA256(path, 512<<20)
	return "file", digest, err
}
