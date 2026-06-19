package patch

import (
	"io"
	"os"

	"github.com/gabstv/go-bsdiff/pkg/bsdiff"
	"github.com/gabstv/go-bsdiff/pkg/bspatch"
)

// ApplyPatch applies a binary patch to a source file to produce a destination file.
func ApplyPatch(srcPath, dstPath, patchPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	patch, err := os.Open(patchPath)
	if err != nil {
		return err
	}
	defer patch.Close()

	dst, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	return bspatch.Reader(src, dst, patch)
}

// GeneratePatch creates a binary patch between old and new files.
func GeneratePatch(oldPath, newPath, patchPath string) error {
	oldData, err := os.ReadFile(oldPath)
	if err != nil {
		return err
	}

	newData, err := os.ReadFile(newPath)
	if err != nil {
		return err
	}

	patchData, err := bsdiff.Bytes(oldData, newData)
	if err != nil {
		return err
	}

	return os.WriteFile(patchPath, patchData, 0644)
}

// ApplyPatchStream applies a patch using streams to be memory efficient.
func ApplyPatchStream(src io.Reader, patch io.Reader, dst io.Writer) error {
	return bspatch.Reader(src, dst, patch)
}
