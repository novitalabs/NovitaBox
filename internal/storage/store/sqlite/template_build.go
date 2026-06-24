package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/novitalabs/NovitaBox/internal/storage/store"
)

func (s *Store) CreateTemplateBuild(ctx context.Context, record store.TemplateBuildRecord) error {
	now := fillRecordTimes(record.CreatedAt.Unix(), record.UpdatedAt.Unix())
	_, err := s.db.ExecContext(ctx, `
INSERT INTO template_builds (build_id, template_id, status, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)`,
		record.ID,
		record.TemplateID,
		string(record.Status),
		now.createdAt,
		now.updatedAt,
	)
	if err != nil {
		return fmt.Errorf("create template build %q: %w", record.ID, err)
	}

	return nil
}

func (s *Store) GetTemplateBuild(ctx context.Context, templateID string, buildID string) (*store.TemplateBuildRecord, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT build_id, template_id, status, created_at, updated_at
FROM template_builds
WHERE template_id = ? AND build_id = ?`, templateID, buildID)

	record, err := scanTemplateBuild(row)
	if err != nil {
		return nil, err
	}

	return &record, nil
}

func (s *Store) GetLatestTemplateBuild(ctx context.Context, templateID string) (*store.TemplateBuildRecord, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT build_id, template_id, status, created_at, updated_at
FROM template_builds
WHERE template_id = ?
ORDER BY created_at DESC, build_id DESC
LIMIT 1`, templateID)

	record, err := scanTemplateBuild(row)
	if err != nil {
		return nil, err
	}

	return &record, nil
}

func (s *Store) ListTemplateBuilds(ctx context.Context, templateID string) ([]store.TemplateBuildRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT build_id, template_id, status, created_at, updated_at
FROM template_builds
WHERE template_id = ?
ORDER BY created_at DESC, build_id DESC`, templateID)
	if err != nil {
		return nil, fmt.Errorf("list template builds for %q: %w", templateID, err)
	}
	defer rows.Close()

	var records []store.TemplateBuildRecord
	for rows.Next() {
		record, err := scanTemplateBuild(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate template builds for %q: %w", templateID, err)
	}

	return records, nil
}

func (s *Store) UpdateTemplateBuildStatus(ctx context.Context, templateID string, buildID string, from store.TemplateBuildStatus, to store.TemplateBuildStatus) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE template_builds
SET status = ?, updated_at = ?
WHERE template_id = ? AND build_id = ? AND status = ?`,
		string(to),
		unixNow(),
		templateID,
		buildID,
		string(from),
	)
	if err != nil {
		return fmt.Errorf("update template build %q status: %w", buildID, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read template build %q update result: %w", buildID, err)
	}
	if affected == 0 {
		return store.ErrNotFound
	}

	return nil
}

func scanTemplateBuild(row scanner) (store.TemplateBuildRecord, error) {
	var record store.TemplateBuildRecord
	var status string
	var createdAt int64
	var updatedAt int64

	if err := row.Scan(
		&record.ID,
		&record.TemplateID,
		&status,
		&createdAt,
		&updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.TemplateBuildRecord{}, store.ErrNotFound
		}
		return store.TemplateBuildRecord{}, fmt.Errorf("scan template build: %w", err)
	}

	record.Status = store.TemplateBuildStatus(status)
	record.CreatedAt = unixTime(createdAt)
	record.UpdatedAt = unixTime(updatedAt)

	return record, nil
}
