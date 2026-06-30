package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

type migration struct {
	version int
	sql     string
}

var migrations = []migration{
	{
		version: 1,
		sql: `
CREATE TABLE sandboxes (
  sandbox_id TEXT PRIMARY KEY,
  state TEXT NOT NULL,
  runtime_type TEXT NOT NULL DEFAULT '',
  template_id TEXT NOT NULL DEFAULT '',
  image_id TEXT NOT NULL DEFAULT '',
  snapshot_id TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE INDEX idx_sandboxes_state ON sandboxes(state);
CREATE INDEX idx_sandboxes_template_id ON sandboxes(template_id);
CREATE INDEX idx_sandboxes_image_id ON sandboxes(image_id);

CREATE TABLE templates (
  template_id TEXT PRIMARY KEY,
  rootfs_path TEXT NOT NULL DEFAULT '',
  memfile_path TEXT NOT NULL DEFAULT '',
  snapfile_path TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE images (
  image_id TEXT PRIMARY KEY,
  rootfs_path TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE snapshots (
  snapshot_id TEXT PRIMARY KEY,
  sandbox_id TEXT NOT NULL,
  rootfs_path TEXT NOT NULL DEFAULT '',
  memfile_path TEXT NOT NULL DEFAULT '',
  snapfile_path TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE INDEX idx_snapshots_sandbox_id ON snapshots(sandbox_id);

CREATE TABLE template_builds (
  build_id TEXT PRIMARY KEY,
  template_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE INDEX idx_template_builds_template_id ON template_builds(template_id);
CREATE INDEX idx_template_builds_status ON template_builds(status);

CREATE TABLE operations (
  operation_id TEXT PRIMARY KEY,
  resource_type TEXT NOT NULL,
  resource_id TEXT NOT NULL,
  action TEXT NOT NULL,
  status TEXT NOT NULL,
  request_hash TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE INDEX idx_operations_resource ON operations(resource_type, resource_id);
CREATE INDEX idx_operations_status ON operations(status);

CREATE TABLE state_transitions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  resource_type TEXT NOT NULL,
  resource_id TEXT NOT NULL,
  from_state TEXT NOT NULL,
  to_state TEXT NOT NULL,
  action TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE INDEX idx_state_transitions_resource ON state_transitions(resource_type, resource_id);
`,
	},
	{
		version: 2,
		sql: `
ALTER TABLE templates ADD COLUMN aliases_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE templates ADD COLUMN names_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE templates ADD COLUMN metadata_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE templates ADD COLUMN public INTEGER NOT NULL DEFAULT 0;
ALTER TABLE templates ADD COLUMN cpu_count INTEGER NOT NULL DEFAULT 1;
ALTER TABLE templates ADD COLUMN memory_mb INTEGER NOT NULL DEFAULT 512;
`,
	},
	{
		version: 3,
		sql: `
ALTER TABLE sandboxes ADD COLUMN network_slot INTEGER NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX idx_sandboxes_network_slot ON sandboxes(network_slot) WHERE network_slot > 0;
`,
	},
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at INTEGER NOT NULL
)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	for _, m := range migrations {
		applied, err := s.hasMigration(ctx, m.version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}

		if err := s.applyMigration(ctx, m); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) hasMigration(ctx context.Context, version int) (bool, error) {
	var found int
	err := s.db.QueryRowContext(ctx, "SELECT 1 FROM schema_migrations WHERE version = ?", version).Scan(&found)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}

	return false, fmt.Errorf("query schema migration %d: %w", version, err)
}

func (s *Store) applyMigration(ctx context.Context, m migration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %d: %w", m.version, err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("apply migration %d: %w", m.version, err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES (?, unixepoch())", m.version); err != nil {
		return fmt.Errorf("record migration %d: %w", m.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %d: %w", m.version, err)
	}

	return nil
}
