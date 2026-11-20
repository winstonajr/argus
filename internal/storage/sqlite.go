package storage

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"argus/internal/checker"
	"argus/internal/monitor"
	_ "modernc.org/sqlite" // register sqlite3 driver
)

type writeItemType int

const (
	itemCheck writeItemType = iota
	itemEvent
)

const hourMillis = 3600 * 1000

type writeItem struct {
	itemType writeItemType
	sysID    string
	kind     string
	check    *checker.Result
	event    *monitor.ChangeEvent
}

// DB encapsulates the SQLite connection pool, batch writer, and maintenance.
type DB struct {
	db        *sql.DB
	queue     chan writeItem
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
	retChecks time.Duration
	retEvents time.Duration
}

// Open initializes the SQLite database with WAL mode, migrations, and background workers.
func Open(path string, retentionChecks, retentionEvents time.Duration) (*DB, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, fmt.Errorf("create storage directory: %w", err)
		}
	}

	dsn := fmt.Sprintf("%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)", path)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}

	if path == ":memory:" {
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
	} else {
		sqlDB.SetMaxOpenConns(5)
		sqlDB.SetMaxIdleConns(2)
	}
	sqlDB.SetConnMaxLifetime(time.Hour)

	if err := initAndMigrateSchema(sqlDB, path); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("init sqlite schema: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	d := &DB{
		db:        sqlDB,
		queue:     make(chan writeItem, 5000),
		ctx:       ctx,
		cancel:    cancel,
		retChecks: retentionChecks,
		retEvents: retentionEvents,
	}

	d.wg.Add(1)
	go d.writerLoop()

	if retentionChecks > 0 || retentionEvents > 0 {
		d.wg.Add(1)
		go d.retentionLoop()
	}

	return d, nil
}

func initAndMigrateSchema(db *sql.DB, dbPath string) error {
	baseSchema := `
CREATE TABLE IF NOT EXISTS checks (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  system_id     TEXT    NOT NULL,
  kind          TEXT    NOT NULL,
  ok            INTEGER NOT NULL,
  status_code   INTEGER,
  latency_ms    INTEGER,
  loss_pct      REAL,
  error_cat     TEXT,
  error_msg     TEXT,
  checked_at    DATETIME NOT NULL,
  checked_at_ms INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS events (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  system_id      TEXT    NOT NULL,
  kind           TEXT    NOT NULL,
  from_status    TEXT    NOT NULL,
  to_status      TEXT    NOT NULL,
  diagnosis      TEXT,
  detail         TEXT,
  occurred_at    DATETIME NOT NULL,
  occurred_at_ms INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS maintenance_overrides (
  system_id  TEXT PRIMARY KEY,
  enabled    INTEGER NOT NULL,
  until_ms   INTEGER NOT NULL DEFAULT 0,
  updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS checks_hourly (
  system_id      TEXT    NOT NULL,
  kind           TEXT    NOT NULL,
  hour_ms        INTEGER NOT NULL,
  total          INTEGER NOT NULL DEFAULT 0,
  ok_count       INTEGER NOT NULL DEFAULT 0,
  degraded_count INTEGER NOT NULL DEFAULT 0,
  sum_latency_ms INTEGER NOT NULL DEFAULT 0,
  max_latency_ms INTEGER NOT NULL DEFAULT 0,
  max_loss_pct   REAL    NOT NULL DEFAULT 0,
  PRIMARY KEY (system_id, kind, hour_ms)
) WITHOUT ROWID;
`
	if _, err := db.Exec(baseSchema); err != nil {
		return err
	}

	var userVersion int
	if err := db.QueryRow("PRAGMA user_version").Scan(&userVersion); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}

	if userVersion < 2 {
		if err := migrateToV2(db, dbPath); err != nil {
			return fmt.Errorf("migrate to v2: %w", err)
		}
	}

	indexes := `
CREATE INDEX IF NOT EXISTS idx_checks_sys_time ON checks(system_id, checked_at_ms);
CREATE INDEX IF NOT EXISTS idx_checks_sys_kind_ms ON checks(system_id, kind, checked_at_ms);
CREATE INDEX IF NOT EXISTS idx_checks_sys_ok_ms ON checks(system_id, ok, checked_at_ms, latency_ms);
CREATE INDEX IF NOT EXISTS idx_events_sys_time ON events(system_id, occurred_at_ms);
CREATE INDEX IF NOT EXISTS idx_events_sys_kind_ms ON events(system_id, kind, occurred_at_ms);
`
	if _, err := db.Exec(indexes); err != nil {
		return err
	}

	return nil
}

func migrateToV2(db *sql.DB, dbPath string) error {
	hasChecksMs, err := hasColumn(db, "checks", "checked_at_ms")
	if err != nil {
		return err
	}
	hasEventsMs, err := hasColumn(db, "events", "occurred_at_ms")
	if err != nil {
		return err
	}
	hasMaintUntil, err := hasColumn(db, "maintenance_overrides", "until_ms")
	if err != nil {
		return err
	}

	var existingRows int
	_ = db.QueryRow("SELECT (SELECT COUNT(*) FROM checks) + (SELECT COUNT(*) FROM events)").Scan(&existingRows)
	if existingRows > 0 && dbPath != "" && dbPath != ":memory:" {
		_, _ = db.Exec("PRAGMA wal_checkpoint(PASSIVE)")
		backupPath := dbPath + ".bak"
		if _, statErr := os.Stat(backupPath); os.IsNotExist(statErr) {
			if bErr := copyFile(dbPath, backupPath); bErr != nil {
				log.Printf("[WARN] failed to create database backup %s: %v", backupPath, bErr)
			} else {
				log.Printf("[INFO] Backup do banco SQLite criado em %s antes da migração v2", backupPath)
			}
		}
	}

	if !hasChecksMs {
		if _, err := db.Exec("ALTER TABLE checks ADD COLUMN checked_at_ms INTEGER NOT NULL DEFAULT 0"); err != nil {
			return fmt.Errorf("alter checks add checked_at_ms: %w", err)
		}
	}
	if !hasEventsMs {
		if _, err := db.Exec("ALTER TABLE events ADD COLUMN occurred_at_ms INTEGER NOT NULL DEFAULT 0"); err != nil {
			return fmt.Errorf("alter events add occurred_at_ms: %w", err)
		}
	}
	if !hasMaintUntil {
		if _, err := db.Exec("ALTER TABLE maintenance_overrides ADD COLUMN until_ms INTEGER NOT NULL DEFAULT 0"); err != nil {
			return fmt.Errorf("alter maintenance_overrides add until_ms: %w", err)
		}
	}

	// Fast SQL backfill using substr(col, 1, 19) which works for both ISO-8601 and Go time.String()
	_, _ = db.Exec(`
UPDATE checks
SET checked_at_ms = CAST(strftime('%s', substr(CAST(checked_at AS TEXT), 1, 19)) AS INTEGER) * 1000
WHERE checked_at_ms = 0 AND strftime('%s', substr(CAST(checked_at AS TEXT), 1, 19)) IS NOT NULL
`)
	_, _ = db.Exec(`
UPDATE events
SET occurred_at_ms = CAST(strftime('%s', substr(CAST(occurred_at AS TEXT), 1, 19)) AS INTEGER) * 1000
WHERE occurred_at_ms = 0 AND strftime('%s', substr(CAST(occurred_at AS TEXT), 1, 19)) IS NOT NULL
`)

	// Populate checks_hourly rollup from existing checks
	_, err = db.Exec(`
INSERT INTO checks_hourly (system_id, kind, hour_ms, total, ok_count, degraded_count, sum_latency_ms, max_latency_ms, max_loss_pct)
SELECT
  system_id,
  kind,
  (checked_at_ms / 3600000) * 3600000 AS hour_ms,
  COUNT(*),
  SUM(CASE WHEN ok = 1 THEN 1 ELSE 0 END),
  SUM(CASE WHEN ok = 1 AND (COALESCE(loss_pct, 0) > 0 OR COALESCE(latency_ms, 0) > 2000) THEN 1 ELSE 0 END),
  SUM(CASE WHEN ok = 1 THEN COALESCE(latency_ms, 0) ELSE 0 END),
  MAX(COALESCE(latency_ms, 0)),
  MAX(COALESCE(loss_pct, 0))
FROM checks
WHERE checked_at_ms > 0
GROUP BY system_id, kind, hour_ms
ON CONFLICT(system_id, kind, hour_ms) DO UPDATE SET
  total = excluded.total,
  ok_count = excluded.ok_count,
  degraded_count = excluded.degraded_count,
  sum_latency_ms = excluded.sum_latency_ms,
  max_latency_ms = excluded.max_latency_ms,
  max_loss_pct = excluded.max_loss_pct
`)
	if err != nil {
		return fmt.Errorf("backfill checks_hourly: %w", err)
	}

	_, err = db.Exec("PRAGMA user_version = 2")
	return err
}

func hasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return false, err
		}
		if strings.EqualFold(name, column) {
			return true, nil
		}
	}
	return false, rows.Err()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// AsyncSaveCheck enqueues a check result for batch writing.
func (d *DB) AsyncSaveCheck(sysID, kind string, res checker.Result) {
	select {
	case d.queue <- writeItem{
		itemType: itemCheck,
		sysID:    sysID,
		kind:     kind,
		check:    &res,
	}:
	default:
		log.Printf("[WARN] SQLite write queue full; dropped check for system %s", sysID)
	}
}

// AsyncSaveEvent enqueues a status change event for writing.
func (d *DB) AsyncSaveEvent(e monitor.ChangeEvent) {
	select {
	case d.queue <- writeItem{
		itemType: itemEvent,
		sysID:    e.SystemID,
		kind:     e.Kind,
		event:    &e,
	}:
	default:
		log.Printf("[WARN] SQLite write queue full; dropped event for system %s", e.SystemID)
	}
}

func (d *DB) writerLoop() {
	defer d.wg.Done()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var batch []writeItem

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := d.writeBatch(batch); err != nil {
			log.Printf("[ERROR] failed to write batch to sqlite: %v", err)
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-d.ctx.Done():
			for {
				select {
				case item := <-d.queue:
					batch = append(batch, item)
					if len(batch) >= 200 {
						flush()
					}
				default:
					flush()
					return
				}
			}

		case item := <-d.queue:
			batch = append(batch, item)
			if len(batch) >= 100 {
				flush()
			}

		case <-ticker.C:
			flush()
		}
	}
}

func (d *DB) writeBatch(batch []writeItem) error {
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	stmtCheck, err := tx.Prepare(`
INSERT INTO checks (system_id, kind, ok, status_code, latency_ms, loss_pct, error_cat, error_msg, checked_at, checked_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`)
	if err != nil {
		return fmt.Errorf("prepare check stmt: %w", err)
	}
	defer stmtCheck.Close()

	stmtHourly, err := tx.Prepare(`
INSERT INTO checks_hourly (system_id, kind, hour_ms, total, ok_count, degraded_count, sum_latency_ms, max_latency_ms, max_loss_pct)
VALUES (?, ?, ?, 1, ?, ?, ?, ?, ?)
ON CONFLICT(system_id, kind, hour_ms) DO UPDATE SET
  total          = checks_hourly.total + 1,
  ok_count       = checks_hourly.ok_count + excluded.ok_count,
  degraded_count = checks_hourly.degraded_count + excluded.degraded_count,
  sum_latency_ms = checks_hourly.sum_latency_ms + excluded.sum_latency_ms,
  max_latency_ms = MAX(checks_hourly.max_latency_ms, excluded.max_latency_ms),
  max_loss_pct   = MAX(checks_hourly.max_loss_pct, excluded.max_loss_pct)
`)
	if err != nil {
		return fmt.Errorf("prepare hourly stmt: %w", err)
	}
	defer stmtHourly.Close()

	stmtEvent, err := tx.Prepare(`
INSERT INTO events (system_id, kind, from_status, to_status, diagnosis, detail, occurred_at, occurred_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
`)
	if err != nil {
		return fmt.Errorf("prepare event stmt: %w", err)
	}
	defer stmtEvent.Close()

	for _, item := range batch {
		if item.itemType == itemCheck && item.check != nil {
			okInt := 0
			if item.check.OK {
				okInt = 1
			}
			latMs := item.check.Latency.Milliseconds()
			checkedUTC := item.check.CheckedAt.UTC()
			checkedMs := checkedUTC.UnixMilli()
			if checkedMs <= 0 {
				checkedUTC = time.Now().UTC()
				checkedMs = checkedUTC.UnixMilli()
			}

			_, err := stmtCheck.Exec(
				item.sysID,
				item.kind,
				okInt,
				item.check.StatusCode,
				latMs,
				item.check.Loss,
				item.check.ErrorCat,
				item.check.Error,
				checkedUTC.Format(time.RFC3339Nano),
				checkedMs,
			)
			if err != nil {
				return fmt.Errorf("insert check: %w", err)
			}

			hourMs := (checkedMs / hourMillis) * hourMillis
			degInt := 0
			sumLat := int64(0)
			if okInt == 1 {
				sumLat = latMs
				if item.check.Loss > 0 || latMs > 2000 {
					degInt = 1
				}
			}
			_, err = stmtHourly.Exec(
				item.sysID,
				item.kind,
				hourMs,
				okInt,
				degInt,
				sumLat,
				latMs,
				item.check.Loss,
			)
			if err != nil {
				return fmt.Errorf("upsert hourly check: %w", err)
			}
		} else if item.itemType == itemEvent && item.event != nil {
			occUTC := item.event.OccurredAt.UTC()
			occMs := occUTC.UnixMilli()
			if occMs <= 0 {
				occUTC = time.Now().UTC()
				occMs = occUTC.UnixMilli()
			}
			_, err := stmtEvent.Exec(
				item.sysID,
				item.kind,
				string(item.event.FromStatus),
				string(item.event.ToStatus),
				item.event.Diagnosis,
				item.event.Detail,
				occUTC.Format(time.RFC3339Nano),
				occMs,
			)
			if err != nil {
				return fmt.Errorf("insert event: %w", err)
			}
		}
	}

	return tx.Commit()
}

func (d *DB) retentionLoop() {
	defer d.wg.Done()

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			if err := d.PurgeOldRecords(); err != nil {
				log.Printf("[WARN] SQLite retention cleanup failed: %v", err)
			}
		}
	}
}

// PurgeOldRecords removes records older than the configured retention window in bounded batches.
func (d *DB) PurgeOldRecords() error {
	nowMs := time.Now().UTC().UnixMilli()

	if d.retChecks > 0 {
		cutoffMs := nowMs - d.retChecks.Milliseconds()
		if err := d.deleteInBatches(
			`DELETE FROM checks WHERE rowid IN (SELECT rowid FROM checks WHERE checked_at_ms < ? LIMIT 5000)`,
			cutoffMs,
		); err != nil {
			return fmt.Errorf("purge checks: %w", err)
		}
	}

	if d.retEvents > 0 {
		cutoffMs := nowMs - d.retEvents.Milliseconds()
		if err := d.deleteInBatches(
			`DELETE FROM events WHERE rowid IN (SELECT rowid FROM events WHERE occurred_at_ms < ? LIMIT 5000)`,
			cutoffMs,
		); err != nil {
			return fmt.Errorf("purge events: %w", err)
		}
		if _, err := d.db.ExecContext(d.ctx, `DELETE FROM checks_hourly WHERE hour_ms < ?`, cutoffMs); err != nil {
			return fmt.Errorf("purge checks_hourly: %w", err)
		}
	}

	_, _ = d.db.ExecContext(d.ctx, `PRAGMA optimize`)
	return nil
}

func (d *DB) deleteInBatches(query string, cutoffMs int64) error {
	for i := 0; i < 100; i++ {
		res, err := d.db.ExecContext(d.ctx, query, cutoffMs)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n < 5000 {
			break
		}
	}
	return nil
}

// Ping checks database connectivity.
func (d *DB) Ping(ctx context.Context) error {
	return d.db.PingContext(ctx)
}

// Close flushes all queued items and closes database.
func (d *DB) Close() error {
	d.cancel()
	d.wg.Wait()
	return d.db.Close()
}
