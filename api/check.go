package api

import (
	"net/http"
	"provision/product"
	"provision/version"
)

// UpdateCheckResponse is the schema for the update check endpoint.
type UpdateCheckResponse struct {
	ShouldUpdate         bool   `json:"should_update"`
	LatestVersion        string `json:"latest_version,omitempty"`
	RequiresFullDownload bool   `json:"requires_full_download"`
}

// HandleCheckUpdate handles GET /products/{product_id}/check
func (server *Server) HandleCheckUpdate(w http.ResponseWriter, r *http.Request) {
	productID := r.PathValue("product_id")
	currentClientVersion := r.URL.Query().Get("current_version")

	p, err := product.GetProduct(r.Context(), server.DB, productID)
	if err != nil {
		server.SendError(w, http.StatusInternalServerError, "DB_ERROR", "Internal server error")
		return
	}
	if p == nil {
		server.SendError(w, http.StatusNotFound, "NOT_FOUND", "Product not found")
		return
	}

	latestVersion, err := version.GetLatestVersion(r.Context(), server.DB, productID)
	if err != nil {
		server.SendError(w, http.StatusInternalServerError, "DB_ERROR", "Internal server error")
		return
	}
	if latestVersion == nil {
		server.SendJSONResponse(w, http.StatusOK, UpdateCheckResponse{ShouldUpdate: false})
		return
	}

	if latestVersion.VersionString == currentClientVersion {
		server.SendJSONResponse(w, http.StatusOK, UpdateCheckResponse{
			ShouldUpdate:  false,
			LatestVersion: latestVersion.VersionString,
		})
		return
	}

	currentProductVersion, err := version.GetVersionByString(r.Context(), server.DB, productID, currentClientVersion)
	if err != nil {
		server.SendError(w, http.StatusInternalServerError, "DB_ERROR", "Internal server error")
		return
	}
	if currentProductVersion == nil {
		server.SendJSONResponse(w, http.StatusOK, UpdateCheckResponse{
			ShouldUpdate:         true,
			LatestVersion:        latestVersion.VersionString,
			RequiresFullDownload: true,
		})
		return
	}

	patchPath, _ := version.FindPatchPath(
		r.Context(),
		server.DB,
		productID,
		currentProductVersion.ID,
		latestVersion.ID,
	)
	server.SendJSONResponse(w, http.StatusOK, UpdateCheckResponse{
		ShouldUpdate:         true,
		LatestVersion:        latestVersion.VersionString,
		RequiresFullDownload: patchPath == nil,
	})
}
