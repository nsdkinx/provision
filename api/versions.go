package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"provision/filesystem"
	"provision/patch"
	"provision/version"
	"strings"

	"github.com/google/uuid"
)

type ManifestEntry struct {
	FilePath   string `json:"file_path"`
	SHA256Hash string `json:"sha256_hash"`
	FileSize   int64  `json:"file_size"`
}

type UpdateRequest struct {
	ProductID      string
	FromVersion    string
	ToVersion      string
	Manifest       []ManifestEntry
	PatchBundleZip string
}

func validatePath(base, path string) (string, error) {
	fullPath := filepath.Join(base, path)
	rel, err := filepath.Rel(base, fullPath)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", fmt.Errorf("illegal file path: %s", path)
	}
	return fullPath, nil
}

// HandleInitialVersion handles POST /products/{product_id}/versions/initial
func (server *Server) HandleInitialVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	productID := r.PathValue("product_id")

	r.Body = http.MaxBytesReader(w, r.Body, server.Config.MaxUploadSize)
	mr, err := r.MultipartReader()
	if err != nil {
		server.SendError(w, http.StatusBadRequest, "INVALID_MULTIPART", "Failed to parse multipart form")
		return
	}

	var versionStr string
	var tempZipFile *os.File

	// Ensure clean-up of temporary files
	defer func() {
		if tempZipFile != nil {
			tempZipFile.Close()
			os.Remove(tempZipFile.Name())
		}
	}()

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			server.SendError(w, http.StatusBadRequest, "PART_ERROR", "Error reading multipart part")
			return
		}

		if part.FormName() == "version" {
			vBytes, _ := io.ReadAll(part)
			versionStr = string(vBytes)
		} else if part.FormName() == "file" {
			tempZipFile, err = os.CreateTemp(server.DataDir, "initial-*.zip")
			if err != nil {
				server.Logger.Error("Failed to create temp zip", slog.String("error", err.Error()))
				server.SendError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
				return
			}
			if _, err := io.Copy(tempZipFile, part); err != nil {
				server.Logger.Error("Failed to write to temp zip", slog.String("error", err.Error()))
				server.SendError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
				return
			}
			tempZipFile.Seek(0, 0)
		}
		part.Close()
	}

	if versionStr == "" || tempZipFile == nil {
		server.SendError(w, http.StatusBadRequest, "MISSING_DATA", "Missing version or file")
		return
	}

	server.Logger.Info("Processing initial version",
		slog.String("product_id", productID),
		slog.String("version", versionStr),
		slog.String("zip_path", tempZipFile.Name()),
	)

	tempDir, err := os.MkdirTemp(server.DataDir, "temp-*")
	if err != nil {
		server.SendError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
		return
	}
	defer os.RemoveAll(tempDir)

	newMasterTemp := filepath.Join(tempDir, "master")
	if err := os.MkdirAll(newMasterTemp, 0755); err != nil {
		server.SendError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
		return
	}

	if err := filesystem.UnpackZip(tempZipFile.Name(), newMasterTemp); err != nil {
		server.Logger.Error("Failed to unpack initial version zip", slog.String("error", err.Error()), slog.String("product_id", productID))
		server.SendError(w, http.StatusBadRequest, "PROCESS_FAILED", "Failed to unpack initial zip")
		return
	}

	ver := &version.Version{
		ID:            uuid.New().String(),
		ProductID:     productID,
		VersionString: versionStr,
	}

	var manifests []*version.FileManifest
	var fileCount int
	err = filepath.Walk(newMasterTemp, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}

		select {
		case <-r.Context().Done():
			return r.Context().Err()
		default:
		}

		relPath, _ := filepath.Rel(newMasterTemp, path)
		hash, _ := filesystem.CalculateSHA256(path)

		// Set Read-Only permission
		os.Chmod(path, 0444)

		m := &version.FileManifest{
			ID:         uuid.New().String(),
			VersionID:  ver.ID,
			FilePath:   relPath,
			FileSize:   info.Size(),
			SHA256Hash: hash,
		}
		fileCount++
		manifests = append(manifests, m)
		return nil
	})

	if err != nil {
		server.Logger.Error("Failed to walk master path", slog.String("error", err.Error()), slog.String("master_path", newMasterTemp))
		server.SendError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
		return
	}

	permVersionPath := filesystem.VersionPath(server.DataDir, productID, versionStr)
	os.MkdirAll(filepath.Dir(permVersionPath), 0755)
	os.RemoveAll(permVersionPath)
	if err := os.Rename(newMasterTemp, permVersionPath); err != nil {
		if err := filesystem.CopyDir(newMasterTemp, permVersionPath); err != nil {
			server.SendError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
			return
		}
	}

	if err := version.SaveVersionComplete(r.Context(), server.DB, ver, manifests, nil); err != nil {
		server.Logger.Error("Failed to save initial version to DB", slog.String("error", err.Error()))
		server.SendError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to save version")
		return
	}

	masterPath := filesystem.MasterPath(server.DataDir, productID)
	if err := filesystem.AtomicSymlink(permVersionPath, masterPath); err != nil {
		server.Logger.Error("Failed to link master path", slog.String("error", err.Error()))
		server.SendError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to swap master symlink")
		return
	}

	// Create master.zip asynchronously
	server.scheduleMasterZipBuild(productID, permVersionPath)

	server.Logger.Info("Initial version processed successfully",
		slog.String("product_id", productID),
		slog.String("version", versionStr),
		slog.Int("file_count", fileCount),
	)

	server.SendJSONResponse(w, http.StatusCreated, ver)
}

// HandleUpdateVersion handles POST /products/{product_id}/versions/update
func (server *Server) HandleUpdateVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	productID := r.PathValue("product_id")

	r.Body = http.MaxBytesReader(w, r.Body, server.Config.MaxUploadSize)
	mr, err := r.MultipartReader()
	if err != nil {
		server.SendError(w, http.StatusBadRequest, "INVALID_MULTIPART", "Failed to parse multipart form")
		return
	}

	updateRequest := UpdateRequest{ProductID: productID}
	var tempBundleFile *os.File

	defer func() {
		if tempBundleFile != nil {
			tempBundleFile.Close()
			os.Remove(tempBundleFile.Name())
		}
	}()

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			server.SendError(w, http.StatusBadRequest, "PART_ERROR", "Error reading multipart part")
			return
		}

		switch part.FormName() {
		case "from_version":
			v, _ := io.ReadAll(part)
			updateRequest.FromVersion = string(v)
		case "to_version":
			v, _ := io.ReadAll(part)
			updateRequest.ToVersion = string(v)
		case "manifest_json":
			if err := json.NewDecoder(part).Decode(&updateRequest.Manifest); err != nil {
				server.SendError(w, http.StatusBadRequest, "INVALID_MANIFEST", "Failed to decode manifest JSON")
				return
			}
		case "patch_bundle":
			tempBundleFile, err = os.CreateTemp(server.DataDir, "update-*.zip")
			if err != nil {
				server.Logger.Error("Failed to create temp zip for update", slog.String("error", err.Error()))
				server.SendError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
				return
			}
			if _, err := io.Copy(tempBundleFile, part); err != nil {
				server.Logger.Error("Failed to write to temp update zip", slog.String("error", err.Error()))
				server.SendError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
				return
			}
			tempBundleFile.Seek(0, 0)
			updateRequest.PatchBundleZip = tempBundleFile.Name()
		}
		part.Close()
	}

	if updateRequest.FromVersion == "" || updateRequest.ToVersion == "" || updateRequest.Manifest == nil || updateRequest.PatchBundleZip == "" {
		server.SendError(w, http.StatusBadRequest, "MISSING_DATA", "Missing required update data")
		return
	}

	server.Logger.Info("Processing version update",
		slog.String("product_id", updateRequest.ProductID),
		slog.String("from_version", updateRequest.FromVersion),
		slog.String("to_version", updateRequest.ToVersion),
	)

	fromVersion, err := version.GetVersionByString(r.Context(), server.DB, updateRequest.ProductID, updateRequest.FromVersion)
	if err != nil || fromVersion == nil {
		server.SendError(w, http.StatusBadRequest, "PROCESS_FAILED", "Source version not found")
		return
	}

	toVersion := &version.Version{
		ID:            uuid.New().String(),
		ProductID:     updateRequest.ProductID,
		VersionString: updateRequest.ToVersion,
	}

	// Prepare Workspace
	wsDir, err := os.MkdirTemp(server.DataDir, "temp-*")
	if err != nil {
		server.SendError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
		return
	}
	defer os.RemoveAll(wsDir)

	extractPath := filepath.Join(wsDir, "extracted")
	if err := filesystem.UnpackZip(updateRequest.PatchBundleZip, extractPath); err != nil {
		server.SendError(w, http.StatusBadRequest, "PROCESS_FAILED", "Failed to unpack patch bundle")
		return
	}

	newMasterTemp := filepath.Join(wsDir, "new_master")
	os.MkdirAll(newMasterTemp, 0755)

	patchStoreTemp := filepath.Join(wsDir, "patch_store")
	os.MkdirAll(patchStoreTemp, 0755)

	currentMaster := filesystem.MasterPath(server.DataDir, updateRequest.ProductID)
	var patches []*version.Patch
	var fileManifests []*version.FileManifest

	for _, entry := range updateRequest.Manifest {
		select {
		case <-r.Context().Done():
			server.SendError(w, http.StatusInternalServerError, "TIMEOUT", "Context cancelled")
			return
		default:
		}

		dstFile, err := validatePath(newMasterTemp, entry.FilePath)
		if err != nil {
			server.SendError(w, http.StatusBadRequest, "INVALID_PATH", err.Error())
			return
		}
		srcFile := filepath.Join(currentMaster, entry.FilePath)
		patchFile := filepath.Join(extractPath, entry.FilePath+".patch")
		newFileInBundle := filepath.Join(extractPath, entry.FilePath)

		os.MkdirAll(filepath.Dir(dstFile), 0755)

		if _, err := os.Stat(patchFile); err == nil {
			if err := patch.ApplyPatch(srcFile, dstFile, patchFile); err != nil {
				server.Logger.Error("Patch apply failed", slog.String("error", err.Error()), slog.String("file", entry.FilePath))
				server.SendError(w, http.StatusBadRequest, "PATCH_FAILED", fmt.Sprintf("Failed to apply patch for %s", entry.FilePath))
				return
			}
			permPatchPath := filepath.Join(patchStoreTemp, entry.FilePath+".patch")
			os.MkdirAll(filepath.Dir(permPatchPath), 0755)
			filesystem.CopyFile(patchFile, permPatchPath)

			hash, _ := filesystem.CalculateSHA256(permPatchPath)
			info, _ := os.Stat(permPatchPath)

			patches = append(patches, &version.Patch{
				ID:              uuid.New().String(),
				ProductID:       updateRequest.ProductID,
				FromVersionID:   fromVersion.ID,
				ToVersionID:     toVersion.ID,
				FilePath:        entry.FilePath,
				PatchSize:       info.Size(),
				PatchFileSHA256: hash,
			})
		} else if info, err := os.Stat(newFileInBundle); err == nil && !info.IsDir() {
			filesystem.CopyFile(newFileInBundle, dstFile)
			permNewPath := filepath.Join(patchStoreTemp, entry.FilePath)
			os.MkdirAll(filepath.Dir(permNewPath), 0755)
			filesystem.CopyFile(newFileInBundle, permNewPath)
		} else if _, err := os.Stat(srcFile); err == nil {
			if err := os.Link(srcFile, dstFile); err != nil {
				filesystem.CopyFile(srcFile, dstFile)
			}
		} else {
			server.SendError(w, http.StatusBadRequest, "MISSING_FILE", fmt.Sprintf("File %s missing", entry.FilePath))
			return
		}

		os.Chmod(dstFile, 0444)

		actualHash, _ := filesystem.CalculateSHA256(dstFile)
		if actualHash != entry.SHA256Hash {
			server.SendError(w, http.StatusBadRequest, "HASH_MISMATCH", fmt.Sprintf("Integrity check failed for %s", entry.FilePath))
			return
		}

		fileManifests = append(fileManifests, &version.FileManifest{
			ID:         uuid.New().String(),
			VersionID:  toVersion.ID,
			FilePath:   entry.FilePath,
			FileSize:   entry.FileSize,
			SHA256Hash: entry.SHA256Hash,
		})
	}

	// Promote Workspace
	permVersionPath := filesystem.VersionPath(server.DataDir, updateRequest.ProductID, updateRequest.ToVersion)
	os.MkdirAll(filepath.Dir(permVersionPath), 0755)
	os.RemoveAll(permVersionPath)
	if err := os.Rename(newMasterTemp, permVersionPath); err != nil {
		if err := filesystem.CopyDir(newMasterTemp, permVersionPath); err != nil {
			server.SendError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to promote version files")
			return
		}
	}

	patchStore := filesystem.PatchStoragePath(server.DataDir, updateRequest.ProductID, updateRequest.FromVersion, updateRequest.ToVersion)
	os.MkdirAll(filepath.Dir(patchStore), 0755)
	os.RemoveAll(patchStore)
	if err := os.Rename(patchStoreTemp, patchStore); err != nil {
		if err := filesystem.CopyDir(patchStoreTemp, patchStore); err != nil {
			server.SendError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to promote patches")
			return
		}
	}

	// Save instructions.json
	instr := map[string]interface{}{
		"from_version": updateRequest.FromVersion,
		"to_version":   updateRequest.ToVersion,
		"manifest":     updateRequest.Manifest,
	}
	instrData, _ := json.Marshal(instr)
	os.WriteFile(filepath.Join(patchStore, "instructions.json"), instrData, 0644)

	// Save Version to database
	if err := version.SaveVersionComplete(r.Context(), server.DB, toVersion, fileManifests, patches); err != nil {
		server.Logger.Error("Failed to save update version to DB", slog.String("error", err.Error()))
		server.SendError(w, http.StatusInternalServerError, "DB_ERROR", "Failed to save version")
		return
	}

	// Symlink hot swap
	if err := filesystem.AtomicSymlink(permVersionPath, currentMaster); err != nil {
		server.Logger.Error("Failed to link master path", slog.String("error", err.Error()))
		server.SendError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to swap master symlink")
		return
	}

	// Schedule post-update asynchronous tasks
	server.scheduleMasterZipBuild(productID, permVersionPath)

	server.SendJSONResponse(w, http.StatusCreated, toVersion)
}

func (server *Server) scheduleMasterZipBuild(productID, permVersionPath string) {
	masterZipPath := filesystem.MasterZipPath(server.DataDir, productID)
	masterZipTmp := fmt.Sprintf("%s.%s.tmp", masterZipPath, uuid.New().String())

	server.RunAsync(func() {
		if err := filesystem.PackZip(permVersionPath, masterZipTmp); err == nil {
			os.Rename(masterZipTmp, masterZipPath)
		} else {
			server.Logger.Error("Failed to pack master zip asynchronously", slog.String("error", err.Error()), slog.String("product_id", productID))
		}
	})
}
