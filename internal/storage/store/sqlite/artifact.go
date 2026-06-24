package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/novitalabs/NovitaBox/internal/storage/store"
)

func (s *Store) CreateImage(ctx context.Context, record store.ImageRecord) error {
	now := fillRecordTimes(record.CreatedAt.Unix(), record.UpdatedAt.Unix())
	_, err := s.db.ExecContext(ctx, `
INSERT INTO images (image_id, rootfs_path, created_at, updated_at)
VALUES (?, ?, ?, ?)`,
		record.ID,
		record.RootfsPath,
		now.createdAt,
		now.updatedAt,
	)
	if err != nil {
		return fmt.Errorf("create image %q: %w", record.ID, err)
	}

	return nil
}

func (s *Store) GetImage(ctx context.Context, imageID string) (*store.ImageRecord, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT image_id, rootfs_path, created_at, updated_at
FROM images
WHERE image_id = ?`, imageID)

	record, err := scanImage(row)
	if err != nil {
		return nil, err
	}

	return &record, nil
}

func (s *Store) ListImages(ctx context.Context) ([]store.ImageRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT image_id, rootfs_path, created_at, updated_at
FROM images
ORDER BY created_at DESC, image_id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	defer rows.Close()

	var records []store.ImageRecord
	for rows.Next() {
		record, err := scanImage(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate images: %w", err)
	}

	return records, nil
}

func (s *Store) DeleteImage(ctx context.Context, imageID string) error {
	return deleteByID(ctx, s.db, "images", "image_id", imageID)
}

func (s *Store) CreateTemplate(ctx context.Context, record store.TemplateRecord) error {
	record = normalizeTemplateRecord(record)
	now := fillRecordTimes(record.CreatedAt.Unix(), record.UpdatedAt.Unix())
	aliasesJSON, err := marshalStringSlice(record.Aliases)
	if err != nil {
		return fmt.Errorf("marshal template %q aliases: %w", record.ID, err)
	}
	namesJSON, err := marshalStringSlice(record.Names)
	if err != nil {
		return fmt.Errorf("marshal template %q names: %w", record.ID, err)
	}
	metadataJSON, err := marshalStringMap(record.Metadata)
	if err != nil {
		return fmt.Errorf("marshal template %q metadata: %w", record.ID, err)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO templates (
  template_id,
  rootfs_path,
  memfile_path,
  snapfile_path,
  aliases_json,
  names_json,
  metadata_json,
  public,
  cpu_count,
  memory_mb,
  created_at,
  updated_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID,
		record.RootfsPath,
		record.MemfilePath,
		record.SnapfilePath,
		aliasesJSON,
		namesJSON,
		metadataJSON,
		boolToInt(record.Public),
		record.CPUCount,
		record.MemoryMB,
		now.createdAt,
		now.updatedAt,
	)
	if err != nil {
		return fmt.Errorf("create template %q: %w", record.ID, err)
	}

	return nil
}

func (s *Store) GetTemplate(ctx context.Context, templateID string) (*store.TemplateRecord, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT template_id, rootfs_path, memfile_path, snapfile_path, aliases_json, names_json, metadata_json, public, cpu_count, memory_mb, created_at, updated_at
FROM templates
WHERE template_id = ?`, templateID)

	record, err := scanTemplate(row)
	if err != nil {
		return nil, err
	}

	return &record, nil
}

func (s *Store) ListTemplates(ctx context.Context) ([]store.TemplateRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT template_id, rootfs_path, memfile_path, snapfile_path, aliases_json, names_json, metadata_json, public, cpu_count, memory_mb, created_at, updated_at
FROM templates
ORDER BY created_at DESC, template_id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list templates: %w", err)
	}
	defer rows.Close()

	var records []store.TemplateRecord
	for rows.Next() {
		record, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate templates: %w", err)
	}

	return records, nil
}

func (s *Store) DeleteTemplate(ctx context.Context, templateID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete template %q: %w", templateID, err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM template_builds WHERE template_id = ?`, templateID); err != nil {
		return fmt.Errorf("delete template builds for %q: %w", templateID, err)
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM templates WHERE template_id = ?`, templateID)
	if err != nil {
		return fmt.Errorf("delete template %q: %w", templateID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read delete template %q result: %w", templateID, err)
	}
	if affected == 0 {
		return store.ErrNotFound
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete template %q: %w", templateID, err)
	}

	return nil
}

func (s *Store) CreateSnapshot(ctx context.Context, record store.SnapshotRecord) error {
	now := fillRecordTimes(record.CreatedAt.Unix(), record.UpdatedAt.Unix())
	_, err := s.db.ExecContext(ctx, `
INSERT INTO snapshots (snapshot_id, sandbox_id, rootfs_path, memfile_path, snapfile_path, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		record.ID,
		record.SandboxID,
		record.RootfsPath,
		record.MemfilePath,
		record.SnapfilePath,
		now.createdAt,
		now.updatedAt,
	)
	if err != nil {
		return fmt.Errorf("create snapshot %q: %w", record.ID, err)
	}

	return nil
}

func (s *Store) GetSnapshot(ctx context.Context, snapshotID string) (*store.SnapshotRecord, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT snapshot_id, sandbox_id, rootfs_path, memfile_path, snapfile_path, created_at, updated_at
FROM snapshots
WHERE snapshot_id = ?`, snapshotID)

	record, err := scanSnapshot(row)
	if err != nil {
		return nil, err
	}

	return &record, nil
}

func (s *Store) ListSnapshotsBySandbox(ctx context.Context, sandboxID string) ([]store.SnapshotRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT snapshot_id, sandbox_id, rootfs_path, memfile_path, snapfile_path, created_at, updated_at
FROM snapshots
WHERE sandbox_id = ?
ORDER BY created_at DESC, snapshot_id DESC`, sandboxID)
	if err != nil {
		return nil, fmt.Errorf("list snapshots for sandbox %q: %w", sandboxID, err)
	}
	defer rows.Close()

	var records []store.SnapshotRecord
	for rows.Next() {
		record, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate snapshots for sandbox %q: %w", sandboxID, err)
	}

	return records, nil
}

func (s *Store) DeleteSnapshot(ctx context.Context, snapshotID string) error {
	return deleteByID(ctx, s.db, "snapshots", "snapshot_id", snapshotID)
}

type recordTimes struct {
	createdAt int64
	updatedAt int64
}

func fillRecordTimes(createdAt int64, updatedAt int64) recordTimes {
	now := unixNow()
	if createdAt <= 0 {
		createdAt = now
	}
	if updatedAt <= 0 {
		updatedAt = createdAt
	}

	return recordTimes{createdAt: createdAt, updatedAt: updatedAt}
}

func scanImage(row scanner) (store.ImageRecord, error) {
	var record store.ImageRecord
	var createdAt int64
	var updatedAt int64

	if err := row.Scan(&record.ID, &record.RootfsPath, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.ImageRecord{}, store.ErrNotFound
		}
		return store.ImageRecord{}, fmt.Errorf("scan image: %w", err)
	}

	record.CreatedAt = unixTime(createdAt)
	record.UpdatedAt = unixTime(updatedAt)

	return record, nil
}

func scanTemplate(row scanner) (store.TemplateRecord, error) {
	var record store.TemplateRecord
	var aliasesJSON string
	var namesJSON string
	var metadataJSON string
	var public int
	var createdAt int64
	var updatedAt int64

	if err := row.Scan(
		&record.ID,
		&record.RootfsPath,
		&record.MemfilePath,
		&record.SnapfilePath,
		&aliasesJSON,
		&namesJSON,
		&metadataJSON,
		&public,
		&record.CPUCount,
		&record.MemoryMB,
		&createdAt,
		&updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.TemplateRecord{}, store.ErrNotFound
		}
		return store.TemplateRecord{}, fmt.Errorf("scan template: %w", err)
	}

	var err error
	record.Aliases, err = unmarshalStringSlice(aliasesJSON)
	if err != nil {
		return store.TemplateRecord{}, fmt.Errorf("scan template aliases: %w", err)
	}
	record.Names, err = unmarshalStringSlice(namesJSON)
	if err != nil {
		return store.TemplateRecord{}, fmt.Errorf("scan template names: %w", err)
	}
	record.Metadata, err = unmarshalStringMap(metadataJSON)
	if err != nil {
		return store.TemplateRecord{}, fmt.Errorf("scan template metadata: %w", err)
	}
	record.Public = public != 0
	record.CreatedAt = unixTime(createdAt)
	record.UpdatedAt = unixTime(updatedAt)
	record = normalizeTemplateRecord(record)

	return record, nil
}

func normalizeTemplateRecord(record store.TemplateRecord) store.TemplateRecord {
	if record.Aliases == nil {
		record.Aliases = []string{}
	}
	if record.Names == nil {
		record.Names = []string{}
	}
	if record.Metadata == nil {
		record.Metadata = map[string]string{}
	}
	if record.CPUCount <= 0 {
		record.CPUCount = 1
	}
	if record.MemoryMB <= 0 {
		record.MemoryMB = 512
	}
	return record
}

func marshalStringSlice(values []string) (string, error) {
	if values == nil {
		values = []string{}
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func unmarshalStringSlice(value string) ([]string, error) {
	if value == "" {
		return []string{}, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(value), &values); err != nil {
		return nil, err
	}
	if values == nil {
		values = []string{}
	}
	return values, nil
}

func marshalStringMap(values map[string]string) (string, error) {
	if values == nil {
		values = map[string]string{}
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func unmarshalStringMap(value string) (map[string]string, error) {
	if value == "" {
		return map[string]string{}, nil
	}
	var values map[string]string
	if err := json.Unmarshal([]byte(value), &values); err != nil {
		return nil, err
	}
	if values == nil {
		values = map[string]string{}
	}
	return values, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func scanSnapshot(row scanner) (store.SnapshotRecord, error) {
	var record store.SnapshotRecord
	var createdAt int64
	var updatedAt int64

	if err := row.Scan(
		&record.ID,
		&record.SandboxID,
		&record.RootfsPath,
		&record.MemfilePath,
		&record.SnapfilePath,
		&createdAt,
		&updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.SnapshotRecord{}, store.ErrNotFound
		}
		return store.SnapshotRecord{}, fmt.Errorf("scan snapshot: %w", err)
	}

	record.CreatedAt = unixTime(createdAt)
	record.UpdatedAt = unixTime(updatedAt)

	return record, nil
}

func deleteByID(ctx context.Context, db *sql.DB, table string, key string, id string) error {
	result, err := db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE %s = ?", table, key), id)
	if err != nil {
		return fmt.Errorf("delete %s %q: %w", table, id, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read %s %q delete result: %w", table, id, err)
	}
	if affected == 0 {
		return store.ErrNotFound
	}

	return nil
}
