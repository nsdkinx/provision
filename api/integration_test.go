package api_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"provision/api"
	"provision/database"
	"provision/product"
	"testing"
)

func createDummyZip(t *testing.T, files map[string]string) *bytes.Buffer {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	for name, body := range files {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.Write([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
	}
	zw.Close()
	return buf
}

func TestCoreOTAFlow(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.OpenDatabase("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("Failed to open memory db: %v", err)
	}
	defer db.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	apiState := &api.Server{
		DB:       db,
		DataDir:  tempDir,
		Logger:   logger,
		Config:   api.Config{MaxUploadSize: 10 * 1024 * 1024},
		AdminKey: "test-admin-key",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/products", apiState.AuthMiddleware(apiState.HandleProducts))
	mux.HandleFunc("POST /api/v1/products/{product_id}/versions/initial", apiState.AuthMiddleware(apiState.HandleInitialVersion))
	mux.HandleFunc("POST /api/v1/products/{product_id}/versions/update", apiState.AuthMiddleware(apiState.HandleUpdateVersion))
	mux.HandleFunc("GET /api/v1/products/{product_id}/check", apiState.HandleCheckUpdate)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// 1. Create a product
	createProdReq := product.ProductCreate{
		ID:   "test_product_1",
		Name: "Test Product",
	}
	createProdBody, _ := json.Marshal(createProdReq)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/products", bytes.NewBuffer(createProdBody))
	req.Header.Set("X-API-Key", "test-admin-key")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("Failed to create product: %v", err)
	}
	var createdProd product.Product
	json.NewDecoder(resp.Body).Decode(&createdProd)
	resp.Body.Close()
	apiKey := createdProd.DeveloperKey

	// 2. Upload initial version (v1.0.0)
	bodyBuf := new(bytes.Buffer)
	mw := multipart.NewWriter(bodyBuf)
	mw.WriteField("version", "1.0.0")
	fw, _ := mw.CreateFormFile("file", "initial.zip")
	zipBuf := createDummyZip(t, map[string]string{"main.exe": "v1 content"})
	io.Copy(fw, zipBuf)
	mw.Close()

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/products/test_product_1/versions/initial", bodyBuf)
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusCreated {
		if resp != nil {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("Failed to upload initial version: status %d, body: %s", resp.StatusCode, string(b))
		}
		t.Fatalf("Failed to upload initial version: %v", err)
	}
	resp.Body.Close()

	// 3. Upload an update (v1.1.0)
	bodyBuf = new(bytes.Buffer)
	mw = multipart.NewWriter(bodyBuf)
	mw.WriteField("from_version", "1.0.0")
	mw.WriteField("to_version", "1.1.0")

	manifest := []api.ManifestEntry{
		{
			FilePath:   "main.exe",
			SHA256Hash: "16ff87cc722d5704aca0597217a2593272254e3bd9fe40428abcab7d1a266f47", // hash for "v1.1 content"
			FileSize:   12,
		},
	}
	manifestBytes, _ := json.Marshal(manifest)
	fw, _ = mw.CreateFormField("manifest_json")
	fw.Write(manifestBytes)

	fw, _ = mw.CreateFormFile("patch_bundle", "update.zip")
	zipBuf = createDummyZip(t, map[string]string{"main.exe": "v1.1 content"})
	io.Copy(fw, zipBuf)
	mw.Close()

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/products/test_product_1/versions/update", bodyBuf)
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusCreated {
		if resp != nil {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("Failed to upload update version: status %d, body: %s", resp.StatusCode, string(b))
		}
		t.Fatalf("Failed to upload update version: %v", err)
	}
	resp.Body.Close()

	// 4. Check for updates
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/products/test_product_1/check?current_version=1.0.0", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Failed to check for updates: %v", err)
	}
	var checkResp api.UpdateCheckResponse
	json.NewDecoder(resp.Body).Decode(&checkResp)
	resp.Body.Close()

	if !checkResp.ShouldUpdate || checkResp.LatestVersion != "1.1.0" {
		t.Fatalf("Unexpected check response: %+v", checkResp)
	}

	// Wait for async tasks to complete
	apiState.Wait()
}
