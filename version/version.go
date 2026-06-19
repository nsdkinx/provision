package version

import (
	"context"
	"database/sql"
	"time"
)

// Version represents a specific release of a Product.
type Version struct {
	ID            string    `json:"id"`
	ProductID     string    `json:"product_id"`
	VersionString string    `json:"version_string"`
	CreatedAt     time.Time `json:"created_at"`
}

// FileManifest represents the state of a file in a specific Version.
type FileManifest struct {
	ID         string `json:"id"`
	VersionID  string `json:"version_id"`
	FilePath   string `json:"file_path"`
	FileSize   int64  `json:"file_size"`
	SHA256Hash string `json:"sha256_hash"`
}

// Patch represents the delta between a file in Version A and Version B.
type Patch struct {
	ID              string `json:"id"`
	ProductID       string `json:"product_id"`
	FromVersionID   string `json:"from_version_id"`
	ToVersionID     string `json:"to_version_id"`
	FilePath        string `json:"file_path"`
	PatchSize       int64  `json:"patch_size"`
	PatchFileSHA256 string `json:"patch_sha256"` // Hash of the .patch file itself
}

// versionTransition represents a single, valid upgrade jump between two versions.
// We use this instead of the Patch struct because we only care about the route,
// not the specific file deltas.
type versionTransition struct {
	FromID string
	ToID   string
}

// SaveVersionComplete inserts a Version along with its file manifests and patches in a transaction.
func SaveVersionComplete(ctx context.Context, db *sql.DB, v *Version, manifests []*FileManifest, patches []*Patch) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Insert version into database
	_, err = tx.ExecContext(
		ctx,
		"INSERT INTO versions (id, product_id, version_string) VALUES (?, ?, ?)",
		v.ID, v.ProductID, v.VersionString,
	)
	if err != nil {
		return err
	}

	// Prepare statement for batch inserts of manifests
	if len(manifests) > 0 {
		statement, err := tx.PrepareContext(
			ctx,
			"INSERT INTO file_manifests (id, version_id, file_path, file_size, sha256_hash) VALUES (?, ?, ?, ?, ?)",
		)
		if err != nil {
			return err
		}
		defer statement.Close()

		for _, manifest := range manifests {
			_, err := statement.ExecContext(
				ctx,
				manifest.ID,
				manifest.VersionID,
				manifest.FilePath,
				manifest.FileSize,
				manifest.SHA256Hash,
			)
			if err != nil {
				return err
			}
		}
	}

	// Prepare statement for batch inserts of patches
	if len(patches) > 0 {
		statement, err := tx.PrepareContext(
			ctx,
			"INSERT INTO patches (id, product_id, from_version_id, to_version_id, file_path, patch_size, patch_sha256) VALUES (?, ?, ?, ?, ?, ?, ?)",
		)
		if err != nil {
			return err
		}
		defer statement.Close()

		for _, patch := range patches {
			_, err := statement.ExecContext(
				ctx,
				patch.ID,
				patch.ProductID,
				patch.FromVersionID,
				patch.ToVersionID,
				patch.FilePath,
				patch.PatchSize,
				patch.PatchFileSHA256,
			)
			if err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

func GetLatestVersion(ctx context.Context, db *sql.DB, productID string) (*Version, error) {
	var ver Version
	err := db.QueryRowContext(ctx, "SELECT id, product_id, version_string, created_at FROM versions WHERE product_id = ? ORDER BY created_at DESC LIMIT 1", productID).Scan(&ver.ID, &ver.ProductID, &ver.VersionString, &ver.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ver, nil
}

// GetVersionByString retrieves a version by its product ID and version string.
func GetVersionByString(ctx context.Context, db *sql.DB, productID, versionString string) (*Version, error) {
	var ver Version
	err := db.QueryRowContext(ctx, "SELECT id, product_id, version_string, created_at FROM versions WHERE product_id = ? AND version_string = ?", productID, versionString).Scan(&ver.ID, &ver.ProductID, &ver.VersionString, &ver.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ver, nil
}

// GetVersionByID retrieves a version by its ID.
func GetVersionByID(ctx context.Context, db *sql.DB, id string) (*Version, error) {
	var ver Version
	err := db.QueryRowContext(ctx, "SELECT id, product_id, version_string, created_at FROM versions WHERE id = ?", id).Scan(&ver.ID, &ver.ProductID, &ver.VersionString, &ver.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ver, nil
}

// getPatchVersionTransitions retrieves all unique, allowed version upgrades for a product.
func getPatchVersionTransitions(ctx context.Context, db *sql.DB, productID string) ([]versionTransition, error) {
	query := "SELECT DISTINCT from_version_id, to_version_id FROM patches WHERE product_id = ?"
	rows, err := db.QueryContext(ctx, query, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var transitions []versionTransition
	for rows.Next() {
		var transition versionTransition
		if err := rows.Scan(&transition.FromID, &transition.ToID); err != nil {
			return nil, err
		}
		transitions = append(transitions, transition)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return transitions, nil
}

// FindPatchPath finds the shortest upgrade route of version IDs between a start and target version.
func FindPatchPath(ctx context.Context, db *sql.DB, productID, fromID, toID string) ([]string, error) {
	transitions, err := getPatchVersionTransitions(ctx, db, productID)
	if err != nil {
		return nil, err
	}

	// adjacency map: "FromVersionID" -> List of "ToVersionIDs"
	upgradesFrom := make(map[string][]string)
	for _, transition := range transitions {
		upgradesFrom[transition.FromID] = append(
			upgradesFrom[transition.FromID],
			transition.ToID,
		)
	}

	type Route struct {
		CurrentID string
		PathTaken []string
	}

	queue := []Route{{
		CurrentID: fromID,
		PathTaken: []string{fromID},
	}}

	hasVisited := map[string]bool{fromID: true}

	for len(queue) > 0 {
		// Pop the first route off the queue
		route := queue[0]
		queue = queue[1:]

		// If we've reached the target version, return the successful path
		if route.CurrentID == toID {
			return route.PathTaken, nil
		}

		// Check all valid next steps from our current version
		for _, nextID := range upgradesFrom[route.CurrentID] {
			if !hasVisited[nextID] {
				hasVisited[nextID] = true

				// Safely copy the path taken so far and append the next step
				newPath := append([]string{}, route.PathTaken...)
				newPath = append(newPath, nextID)

				// Add this extended route to the back of the queue
				queue = append(queue, Route{
					CurrentID: nextID,
					PathTaken: newPath,
				})
			}
		}
	}

	// Queue exhausted, no valid upgrade path exists
	return nil, nil
}
