package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigrationFiveSeparatesLegacyOverlayBDDigest(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "novitabox.db")
	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	_, err = db.ExecContext(ctx, `
CREATE TABLE schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at INTEGER NOT NULL
);
INSERT INTO schema_migrations(version, applied_at) VALUES (1, 0), (2, 0), (3, 0), (4, 0);
CREATE TABLE sandboxes (
  sandbox_id TEXT PRIMARY KEY,
  rootfs_provider TEXT NOT NULL DEFAULT 'directory',
  rootfs_source_ref TEXT NOT NULL DEFAULT '',
  rootfs_snapshot_key TEXT NOT NULL DEFAULT ''
);
INSERT INTO sandboxes(sandbox_id, rootfs_provider, rootfs_source_ref)
VALUES
  ('sbx-digest', 'overlaybd', 'sha256:resolved'),
  ('sbx-tag', 'overlaybd', 'registry.example/team/image:tag');
`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("prepare legacy database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	st, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("migrate legacy database: %v", err)
	}
	defer st.Close()

	assertLegacyRootfsMetadata(t, st.db, "sbx-digest", "sha256:resolved", "sha256:resolved")
	assertLegacyRootfsMetadata(t, st.db, "sbx-tag", "registry.example/team/image:tag", "")
}

func assertLegacyRootfsMetadata(t *testing.T, db *sql.DB, sandboxID, wantRef, wantDigest string) {
	t.Helper()
	var ref string
	var digest string
	if err := db.QueryRow(`
SELECT rootfs_source_ref, rootfs_source_digest
FROM sandboxes
WHERE sandbox_id = ?`, sandboxID).Scan(&ref, &digest); err != nil {
		t.Fatalf("query sandbox %q rootfs metadata: %v", sandboxID, err)
	}
	if ref != wantRef || digest != wantDigest {
		t.Fatalf("sandbox %q rootfs metadata = (%q, %q), want (%q, %q)", sandboxID, ref, digest, wantRef, wantDigest)
	}
}
