package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"provision/filesystem"
	"provision/product"
	"strconv"
)

// HandleProducts handles GET and POST for /products
func (server *Server) HandleProducts(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 100 {
			limit = 100
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		if offset < 0 {
			offset = 0
		}

		products, err := product.ListProducts(r.Context(), server.DB, limit, offset)
		if err != nil {
			server.Logger.Error("DB error listing products", slog.String("error", err.Error()))
			server.SendError(w, http.StatusInternalServerError, "DB_ERROR", "Internal server error")
			return
		}

		total, err := product.CountProducts(r.Context(), server.DB)
		if err != nil {
			server.Logger.Error("DB error counting products", slog.String("error", err.Error()))
			server.SendError(w, http.StatusInternalServerError, "DB_ERROR", "Internal server error")
			return
		}

		server.SendJSONResponse(w, http.StatusOK, map[string]interface{}{
			"total": total,
			"items": products,
		})
		return
	}

	if r.Method == http.MethodPost {
		var in product.ProductCreate
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			server.SendError(w, http.StatusBadRequest, "INVALID_BODY", "Invalid request body")
			return
		}

		if !product.ValidateProductID(in.ID) {
			server.SendError(w, http.StatusBadRequest, "INVALID_ID", "Invalid product ID format")
			return
		}

		plainKey := product.GenerateDeveloperKey()
		hash := sha256.Sum256([]byte(plainKey))
		hashedKey := hex.EncodeToString(hash[:])

		p := &product.Product{
			ID:           in.ID,
			Name:         in.Name,
			DeveloperKey: hashedKey,
		}

		if err := product.CreateProduct(r.Context(), server.DB, p); err != nil {
			server.Logger.Error("Failed to create product in DB", slog.String("error", err.Error()), slog.String("product_id", in.ID))
			server.SendError(w, http.StatusBadRequest, "CREATE_FAILED", "Failed to create product")
			return
		}

		// Return the unhashed developer key only once during creation
		p.DeveloperKey = plainKey
		server.SendJSONResponse(w, http.StatusCreated, p)
		return
	}

	w.WriteHeader(http.StatusMethodNotAllowed)
}

// HandleProductDelete handles DELETE /products/{id}
func (server *Server) HandleProductDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	productID := r.PathValue("id")
	apiKey := r.Header.Get("X-API-Key")

	if !product.ValidateProductKey(r.Context(), server.DB, productID, apiKey) {
		server.SendError(w, http.StatusForbidden, "FORBIDDEN", "Invalid API Key or product not found")
		return
	}

	if err := product.DeleteProduct(r.Context(), server.DB, productID); err != nil {
		server.Logger.Error("DB error deleting product", slog.String("error", err.Error()), slog.String("product_id", productID))
		server.SendError(w, http.StatusInternalServerError, "DB_ERROR", "Internal server error")
		return
	}

	productPath := filesystem.ProductPath(server.DataDir, productID)
	if err := os.RemoveAll(productPath); err != nil {
		server.Logger.Error("Failed to delete product files", slog.String("error", err.Error()), slog.String("product_id", productID))
	}

	w.WriteHeader(http.StatusNoContent)
}
