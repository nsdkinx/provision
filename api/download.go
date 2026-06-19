package api

import (
	"fmt"
	"net/http"
	"os"
	"provision/filesystem"
)

// HandleDownloadLatest handles GET /products/{product_id}/download
func (server *Server) HandleDownloadLatest(w http.ResponseWriter, r *http.Request) {
	productID := r.PathValue("product_id")
	masterZipPath := filesystem.MasterZipPath(server.DataDir, productID)

	if _, err := os.Stat(masterZipPath); os.IsNotExist(err) {
		server.SendError(w, http.StatusNotFound, "NOT_FOUND", "Product not found or no master copy available")
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s_latest.zip", productID))
	http.ServeFile(w, r, masterZipPath)
}
