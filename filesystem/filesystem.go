package filesystem

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Path helpers
func ProductPath(dataDir, productID string) string {
	return filepath.Join(dataDir, productID)
}

func MasterPath(dataDir, productID string) string {
	return filepath.Join(ProductPath(dataDir, productID), "master")
}

func MasterZipPath(dataDir, productID string) string {
	return filepath.Join(ProductPath(dataDir, productID), "master.zip")
}

func VersionsPath(dataDir, productID string) string {
	return filepath.Join(ProductPath(dataDir, productID), "versions")
}

func VersionPath(dataDir, productID, ver string) string {
	return filepath.Join(VersionsPath(dataDir, productID), ver)
}

func PatchesPath(dataDir, productID string) string {
	return filepath.Join(ProductPath(dataDir, productID), "patches")
}

func PatchStoragePath(dataDir, productID, fromVersion, toVersion string) string {
	return filepath.Join(PatchesPath(dataDir, productID), fmt.Sprintf("%s-to-%s", fromVersion, toVersion))
}

// CalculateSHA256 computes the SHA256 hash of a file.
func CalculateSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// UnpackZip extracts a zip file to a destination directory.
func UnpackZip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		fpath := filepath.Join(dest, f.Name)

		// Check for ZipSlip vulnerability
		if !filepath.IsLocal(f.Name) {
			return fmt.Errorf("illegal file path: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return err
		}

		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}

		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()

		if err != nil {
			return err
		}
	}
	return nil
}

// PackZip creates a zip file from a source directory.
func PackZip(srcDir, destZip string) error {
	zipFile, err := os.Create(destZip)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	archive := zip.NewWriter(zipFile)
	defer archive.Close()

	err = filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relPath)
		header.Method = zip.Deflate

		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(writer, file)
		return err
	})

	return err
}

// AtomicSymlink atomically updates a symlink at linkPath to point to targetPath.
func AtomicSymlink(targetPath, linkPath string) error {
	parent := filepath.Dir(linkPath)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return err
	}

	// Create a temporary symlink pointing to the target
	tmpLink := linkPath + "_tmp"
	if err := os.RemoveAll(tmpLink); err != nil {
		return err
	}

	relTarget, err := filepath.Rel(parent, targetPath)
	if err != nil {
		relTarget = targetPath // fallback
	}

	if err := os.Symlink(relTarget, tmpLink); err != nil {
		// Fallback for Windows or systems where symlinks fail
		oldLink := linkPath + "_old"
		os.RemoveAll(oldLink)

		if _, err := os.Stat(linkPath); err == nil {
			if err := os.Rename(linkPath, oldLink); err != nil {
				return err
			}
		}

		if err := os.Rename(targetPath, linkPath); err != nil {
			os.Rename(oldLink, linkPath) // rollback
			return err
		}

		os.RemoveAll(oldLink)
		return nil
	}

	info, err := os.Lstat(linkPath)
	if err == nil {
		if info.Mode()&os.ModeSymlink == 0 && info.IsDir() {
			os.RemoveAll(linkPath)
		}
	}

	if err := os.Rename(tmpLink, linkPath); err != nil {
		os.Remove(tmpLink)
		return err
	}

	return nil
}

// CopyDir recursively copies a directory.
func CopyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, _ := filepath.Rel(src, path)
		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		return CopyFile(path, dstPath)
	})
}

// CopyFile copies a file from src to dst.
func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
