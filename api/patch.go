package api

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"provision/filesystem"
	"provision/version"
)

// PatchStep represents a single step in a sequential patch update.
type PatchStep struct {
	Step        int    `json:"step"`
	FromVersion string `json:"from_version"`
	ToVersion   string `json:"to_version"`
	Directory   string `json:"directory"`
}

// CompositeInstructions is the schema for multi-step patch instructions.
type CompositeInstructions struct {
	ProductID   string      `json:"product_id"`
	FromVersion string      `json:"from_version"`
	ToVersion   string      `json:"to_version"`
	Steps       []PatchStep `json:"steps"`
}

// HandlePatch handles GET /products/{product_id}/patch
func (server *Server) HandlePatch(w http.ResponseWriter, r *http.Request) {
	productID := r.PathValue("product_id")
	fromVersion := r.URL.Query().Get("from_version")
	toVersion := r.URL.Query().Get("to_version")

	fromV, err := version.GetVersionByString(r.Context(), server.DB, productID, fromVersion)
	if err != nil || fromV == nil {
		server.SendError(w, http.StatusNotFound, "NOT_FOUND", "Version not found")
		return
	}

	toV, err := version.GetVersionByString(r.Context(), server.DB, productID, toVersion)
	if err != nil || toV == nil {
		server.SendError(w, http.StatusNotFound, "NOT_FOUND", "Version not found")
		return
	}

	path, err := version.FindPatchPath(r.Context(), server.DB, productID, fromV.ID, toV.ID)
	if err != nil || path == nil {
		server.SendError(w, http.StatusNotFound, "NOT_FOUND", "No patch path found")
		return
	}

	tempZip, err := os.CreateTemp(server.DataDir, "patch-bundle-*.zip")
	if err != nil {
		server.SendError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create temp patch file")
		return
	}
	defer os.Remove(tempZip.Name())

	if err := server.buildPatchBundle(r.Context(), productID, fromVersion, toVersion, path, tempZip); err != nil {
		server.Logger.Error("Failed to build patch bundle",
			slog.String("error", err.Error()),
			slog.String("product_id", productID),
			slog.String("from", fromVersion),
			slog.String("to", toVersion),
		)
		tempZip.Close()
		server.SendError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to build patch bundle")
		return
	}
	tempZip.Close()

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=patch_%s_to_%s.zip", fromVersion, toVersion))
	http.ServeFile(w, r, tempZip.Name())
}

func (server *Server) buildPatchBundle(
	ctx context.Context,
	productID string,
	fromVersion string,
	toVersion string,
	versionIDs []string,
	w io.Writer,
) error {
	archive := zip.NewWriter(w)
	defer archive.Close()

	var patchSteps []PatchStep

	for i := 0; i < len(versionIDs)-1; i++ {
		fromID := versionIDs[i]
		toID := versionIDs[i+1]

		fromVersion, err := version.GetVersionByID(ctx, server.DB, fromID)
		if err != nil || fromVersion == nil {
			return fmt.Errorf("failed to get source version %s: %v", fromID, err)
		}

		toVersion, err := version.GetVersionByID(ctx, server.DB, toID)
		if err != nil || toVersion == nil {
			return fmt.Errorf("failed to get target version %s: %v", toID, err)
		}

		patchFolder := filesystem.PatchStoragePath(
			server.DataDir,
			productID,
			fromVersion.VersionString,
			toVersion.VersionString,
		)
		stepFolderName := fmt.Sprintf("step_%d_%s_to_%s", i, fromVersion.VersionString, toVersion.VersionString)

		err = filepath.Walk(patchFolder, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			relPath, _ := filepath.Rel(patchFolder, path)
			header, _ := zip.FileInfoHeader(info)
			header.Name = filepath.ToSlash(filepath.Join(stepFolderName, relPath))
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

		if err != nil {
			return err
		}

		patchSteps = append(patchSteps, PatchStep{
			Step:        i,
			FromVersion: fromVersion.VersionString,
			ToVersion:   toVersion.VersionString,
			Directory:   stepFolderName,
		})
	}

	// Add composite_instructions.json
	instructions := CompositeInstructions{
		ProductID:   productID,
		FromVersion: fromVersion,
		ToVersion:   toVersion,
		Steps:       patchSteps,
	}
	instructionData, _ := json.Marshal(instructions)
	file, err := archive.Create("composite_instructions.json")
	if err != nil {
		return err
	}
	_, err = file.Write(instructionData)
	return err
}
