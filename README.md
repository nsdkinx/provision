# Provision

Advanced OTA update server for commercial software.

Provision is a robust, self-hosted Over-The-Air (OTA) update server designed to manage and distribute software updates efficiently. By leveraging binary diffing and intelligent graph-based patch pathfinding, Provision ensures that your clients download the absolute minimum payload required to stay up-to-date.

* **Intelligent patch pathfinding:** Graph-based BFS routing for seamless multi-version jumps.
* **Binary diffing:** Drastically reduced update payload sizes using `bsdiff`.
* **Atomic deployments:** Zero-downtime internal state changes leveraging atomic symlink swaps.
* **Multi-product support:** Manage multiple separate applications from a single instance, each secured with isolated developer API keys.

---

Instead of forcing users to re-download an entire application for a minor bug fix, Provision calculates binary deltas. When a developer uploads an update, the system relies on the `bsdiff` algorithm to compare the new files against the existing master copy. The resulting patch files contain only the exact byte-level differences. Clients download these compact diffs and apply them locally using `bspatch`, reducing bandwidth consumption by up to 90%.

Software clients do not always update sequentially. If a client is currently on `v1.0` and the latest release is `v4.1.3`, there may not be a direct, pre-computed `1.0 -> 4.1.3` patch bundle available. 

Provision solves this elegantly using **Patch Pathfinding**. Under the hood, the server maintains an adjacency graph of all known version transitions (e.g., `1.0 -> 1.1`, `1.1 -> 1.1.1`). When an outdated client requests an update, Provision utilizes a Breadth-First Search (BFS) algorithm to dynamically discover the shortest sequential upgrade route. It then packages the required intermediate step-by-step patches into a single, cohesive `.zip` bundle, allowing the client to sequentially fast-forward to the latest version in one operation.

---

## Installation & Deployment

Provision is built in Go and designed for zero-dependency deployments.

### Docker
The repository includes a ready-to-use `Dockerfile` and `docker-compose.yml` for isolated containerized environments.

### No Docker

For direct host deployments, a systemd unit file is provided.

1. **Build the binary:**
```bash
go build -o provision-server ./cmd/server
sudo mv provision-server /usr/local/bin/

```


2. **Install the service:**
```bash
sudo cp init/provision.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now provision

```



---

## Configuration

Configure the server via Environment Variables. In Docker, these are defined in `docker-compose.yml`. For systemd deployments, place them in `/etc/provision/provision.conf`:

| Variable | Default | Description |
| --- | --- | --- |
| `PORT` | `8000` | Port for the HTTP API |
| `DATA_DIR` | `/var/lib/provision/storage` | Directory to store master binaries, zips, and patches |
| `DATABASE_PATH` | `/var/lib/provision/provision.db` | Filepath to the SQLite database |
| `ADMIN_KEY` | *(None)* | Global API key required to create/delete products globally |
| `MAX_UPLOAD_SIZE` | `524288000` | Max file upload size in bytes (Default: 500MB) |
| `LOG_LEVEL` | `info` | Logging verbosity (`debug`, `info`, `warn`, `error`) |

---

## API Overview

The server exposes a JSON HTTP API authenticated via the `X-API-Key` header.

* `GET /api/v1/products` - List all products (Requires `ADMIN_KEY`).
* `POST /api/v1/products` - Create a new product.
* `DELETE /api/v1/products/{id}` - Delete a product and its assets.
* `POST /api/v1/products/{id}/versions/initial` - Upload the base `v1.0` zip bundle.
* `POST /api/v1/products/{id}/versions/update` - Upload a delta update bundle with its manifest against a previous version.
* `GET /api/v1/products/{id}/check?current_version=X` - Check if updates are available and determine if a full download or patch path is required.
* `GET /api/v1/products/{id}/patch?from_version=X&to_version=Y` - Download a dynamically built multi-step patch bundle.
* `GET /api/v1/products/{id}/download` - Download the latest full master zip.

---

## License

This project is licensed under the MIT License.
