CREATE TABLE IF NOT EXISTS products (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	developer_key TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS versions (
	id TEXT PRIMARY KEY,
	product_id TEXT NOT NULL,
	version_string TEXT NOT NULL,
	created_at TIMESTAMP DEFAULT (STRFTIME('%Y-%m-%d %H:%M:%f', 'NOW')),
	FOREIGN KEY (product_id) REFERENCES products(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS file_manifests (
	id TEXT PRIMARY KEY,
	version_id TEXT NOT NULL,
	file_path TEXT NOT NULL,
	file_size INTEGER NOT NULL,
	sha256_hash TEXT NOT NULL,
	FOREIGN KEY (version_id) REFERENCES versions(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS patches (
	id TEXT PRIMARY KEY,
	product_id TEXT NOT NULL,
	from_version_id TEXT NOT NULL,
	to_version_id TEXT NOT NULL,
	file_path TEXT NOT NULL,
	patch_size INTEGER NOT NULL,
	patch_sha256 TEXT NOT NULL,
	FOREIGN KEY (product_id) REFERENCES products(id) ON DELETE CASCADE,
	FOREIGN KEY (from_version_id) REFERENCES versions(id) ON DELETE CASCADE,
	FOREIGN KEY (to_version_id) REFERENCES versions(id) ON DELETE CASCADE
);
