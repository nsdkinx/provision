package product

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"regexp"
)

var validProductID = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

type Product struct {
	ID           string `json:"id"`                      // com.yutaredux.telepower
	Name         string `json:"name"`                    // TelePower
	DeveloperKey string `json:"developer_key,omitempty"` // hashed API key
}

// ProductCreate is the schema for creating a new product.
type ProductCreate struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// GenerateDeveloperKey creates a random hex ID used for developer keys.
func GenerateDeveloperKey() string {
	newKey := make([]byte, 16)
	rand.Read(newKey)
	return hex.EncodeToString(newKey)
}

// ValidateProductID checks if the product ID is in a valid format.
func ValidateProductID(id string) bool {
	return validProductID.MatchString(id)
}

func CreateProduct(ctx context.Context, db *sql.DB, p *Product) error {
	_, err := db.ExecContext(
		ctx,
		"INSERT INTO products (id, name, developer_key) VALUES (?, ?, ?)",
		p.ID, p.Name, p.DeveloperKey,
	)
	return err
}

func GetProduct(ctx context.Context, db *sql.DB, id string) (*Product, error) {
	var p Product
	err := db.QueryRowContext(
		ctx,
		"SELECT id, name, developer_key FROM products WHERE id = ?", id,
	).Scan(&p.ID, &p.Name, &p.DeveloperKey)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func ListProducts(ctx context.Context, db *sql.DB, limit, offset int) ([]Product, error) {
	productRows, err := db.QueryContext(
		ctx,
		"SELECT id, name FROM products LIMIT ? OFFSET ?",
		limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer productRows.Close()

	var products []Product
	for productRows.Next() {
		var p Product
		if err := productRows.Scan(&p.ID, &p.Name); err != nil {
			return nil, err
		}
		products = append(products, p)
	}
	return products, nil
}

// CountProducts returns the total number of products.
func CountProducts(ctx context.Context, db *sql.DB) (int, error) {
	var count int
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM products").Scan(&count)
	return count, err
}

// DeleteProduct deletes a product.
func DeleteProduct(ctx context.Context, db *sql.DB, id string) error {
	_, err := db.ExecContext(ctx, "DELETE FROM products WHERE id = ?", id)
	return err
}

// ValidateProductKey checks if the provided key matches the product's key.
func ValidateProductKey(ctx context.Context, db *sql.DB, productID, apiKey string) bool {
	p, err := GetProduct(ctx, db, productID)
	if err != nil || p == nil {
		return false
	}

	hash := sha256.Sum256([]byte(apiKey))
	hashedKey := hex.EncodeToString(hash[:])
	return subtle.ConstantTimeCompare([]byte(p.DeveloperKey), []byte(hashedKey)) == 1
}
