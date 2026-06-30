package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/novitalabs/NovitaBox/internal/sandbox"
	"github.com/novitalabs/NovitaBox/internal/storage/store"
)

func (s *Store) CreateSandbox(ctx context.Context, record store.SandboxRecord) error {
	now := unixNow()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = unixTime(now)
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = record.CreatedAt
	}

	_, err := s.db.ExecContext(ctx, `
	INSERT INTO sandboxes (
	  sandbox_id, state, runtime_type, template_id, image_id, snapshot_id, network_slot, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID,
		string(record.State),
		record.RuntimeType,
		record.TemplateID,
		record.ImageID,
		record.SnapshotID,
		record.NetworkSlot,
		record.CreatedAt.Unix(),
		record.UpdatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("create sandbox %q: %w", record.ID, err)
	}

	return nil
}

func (s *Store) GetSandbox(ctx context.Context, sandboxID string) (*store.SandboxRecord, error) {
	row := s.db.QueryRowContext(ctx, `
	SELECT sandbox_id, state, runtime_type, template_id, image_id, snapshot_id, network_slot, created_at, updated_at
	FROM sandboxes
	WHERE sandbox_id = ?`, sandboxID)

	record, err := scanSandbox(row)
	if err != nil {
		return nil, err
	}

	return &record, nil
}

func (s *Store) ListSandboxes(ctx context.Context) ([]store.SandboxRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
	SELECT sandbox_id, state, runtime_type, template_id, image_id, snapshot_id, network_slot, created_at, updated_at
	FROM sandboxes
	ORDER BY created_at DESC, sandbox_id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", err)
	}
	defer rows.Close()

	var records []store.SandboxRecord
	for rows.Next() {
		record, err := scanSandbox(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sandboxes: %w", err)
	}

	return records, nil
}

func (s *Store) UpdateSandboxState(ctx context.Context, sandboxID string, from, to sandbox.State, action string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sandbox state update: %w", err)
	}
	defer tx.Rollback()

	now := unixNow()
	result, err := tx.ExecContext(ctx, `
UPDATE sandboxes
SET state = ?, updated_at = ?
WHERE sandbox_id = ? AND state = ?`, string(to), now, sandboxID, string(from))
	if err != nil {
		return fmt.Errorf("update sandbox %q state: %w", sandboxID, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read sandbox %q update result: %w", sandboxID, err)
	}
	if affected == 0 {
		return store.ErrNotFound
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO state_transitions (
  resource_type, resource_id, from_state, to_state, action, created_at
) VALUES (?, ?, ?, ?, ?, ?)`, "sandbox", sandboxID, string(from), string(to), action, now); err != nil {
		return fmt.Errorf("record sandbox %q state transition: %w", sandboxID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sandbox %q state update: %w", sandboxID, err)
	}

	return nil
}

func (s *Store) AssignSandboxNetworkSlot(ctx context.Context, sandboxID string, maxSlot uint32) (uint32, error) {
	if maxSlot == 0 {
		return 0, fmt.Errorf("max network slot must be greater than 0")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin sandbox network slot assignment: %w", err)
	}
	defer tx.Rollback()

	var existing uint32
	err = tx.QueryRowContext(ctx, "SELECT network_slot FROM sandboxes WHERE sandbox_id = ?", sandboxID).Scan(&existing)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, store.ErrNotFound
		}
		return 0, fmt.Errorf("query sandbox %q network slot: %w", sandboxID, err)
	}
	if existing > 0 {
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("commit sandbox %q existing network slot: %w", sandboxID, err)
		}
		return existing, nil
	}

	rows, err := tx.QueryContext(ctx, "SELECT network_slot FROM sandboxes WHERE network_slot > 0 ORDER BY network_slot")
	if err != nil {
		return 0, fmt.Errorf("list assigned network slots: %w", err)
	}
	used := make(map[uint32]struct{})
	for rows.Next() {
		var slot uint32
		if err := rows.Scan(&slot); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan assigned network slot: %w", err)
		}
		used[slot] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate assigned network slots: %w", err)
	}
	rows.Close()

	var slot uint32
	for candidate := uint32(1); candidate <= maxSlot; candidate++ {
		if _, ok := used[candidate]; !ok {
			slot = candidate
			break
		}
	}
	if slot == 0 {
		return 0, fmt.Errorf("no sandbox network slots available")
	}

	now := unixNow()
	result, err := tx.ExecContext(ctx, `
	UPDATE sandboxes
	SET network_slot = ?, updated_at = ?
	WHERE sandbox_id = ? AND network_slot = 0`, slot, now, sandboxID)
	if err != nil {
		return 0, fmt.Errorf("assign sandbox %q network slot: %w", sandboxID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read sandbox %q network slot assignment result: %w", sandboxID, err)
	}
	if affected == 0 {
		return 0, store.ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit sandbox %q network slot assignment: %w", sandboxID, err)
	}

	return slot, nil
}

func (s *Store) ReleaseSandboxNetworkSlot(ctx context.Context, sandboxID string) error {
	result, err := s.db.ExecContext(ctx, `
	UPDATE sandboxes
	SET network_slot = 0, updated_at = ?
	WHERE sandbox_id = ?`, unixNow(), sandboxID)
	if err != nil {
		return fmt.Errorf("release sandbox %q network slot: %w", sandboxID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read sandbox %q network slot release result: %w", sandboxID, err)
	}
	if affected == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteSandbox(ctx context.Context, sandboxID string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM sandboxes WHERE sandbox_id = ?", sandboxID)
	if err != nil {
		return fmt.Errorf("delete sandbox %q: %w", sandboxID, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read sandbox %q delete result: %w", sandboxID, err)
	}
	if affected == 0 {
		return store.ErrNotFound
	}

	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanSandbox(row scanner) (store.SandboxRecord, error) {
	var record store.SandboxRecord
	var state string
	var createdAt int64
	var updatedAt int64

	if err := row.Scan(
		&record.ID,
		&state,
		&record.RuntimeType,
		&record.TemplateID,
		&record.ImageID,
		&record.SnapshotID,
		&record.NetworkSlot,
		&createdAt,
		&updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.SandboxRecord{}, store.ErrNotFound
		}
		return store.SandboxRecord{}, fmt.Errorf("scan sandbox: %w", err)
	}

	record.State = sandbox.State(state)
	record.CreatedAt = unixTime(createdAt)
	record.UpdatedAt = unixTime(updatedAt)

	return record, nil
}
