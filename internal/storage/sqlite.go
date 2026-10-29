package storage

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite" // register sqlite3 driver
	"sentinela/internal/checker"
	"sentinela/internal/monitor"
)

type writeItemType int

const (
	itemCheck writeItemType = iota
	itemEvent
)

type writeItem struct {
	itemType writeItemType
	sysID    string
	kind     string
	check    *checker.Result
	event    *monitor.ChangeEvent
}

// DB encapsulates the SQLite connection pool, batch writer, and maintenance.
type DB struct {
	db          *sql.DB
	queue       chan writeItem
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
	retChecks   time.Duration
	retEvents   time.Duration
}

// Open initializes the SQLite database with WAL mode and tables.
func Open(path string, retentionChecks, retentionEvents time.Duration) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create storage directory: %w", err)
	}

	dsn := fmt.Sprintf("%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)", path)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}

	// Single writer connection pattern for SQLite to avoid lock contention
	sqlDB.SetMaxOpenConns(5)
	sqlDB.SetMaxIdleConns(2)
	sqlDB.SetConnMaxLifetime(time.Hour)

	if err := initSchema(sqlDB); err != nil {
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

func initSchema(db *sql.DB) error {
	schema := `
CREATE TABLE IF NOT EXISTS checks (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  system_id   TEXT    NOT NULL,
  kind        TEXT    NOT NULL,
  ok          INTEGER NOT NULL,
  status_code INTEGER,
  latency_ms  INTEGER,
  loss_pct    REAL,
  error_cat   TEXT,
  error_msg   TEXT,
  checked_at  DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_checks_sys_time ON checks(system_id, checked_at);
CREATE INDEX IF NOT EXISTS idx_checks_sys_ok_time ON checks(system_id, ok, checked_at);

CREATE TABLE IF NOT EXISTS events (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  system_id   TEXT    NOT NULL,
  kind        TEXT    NOT NULL,
  from_status TEXT    NOT NULL,
  to_status   TEXT    NOT NULL,
  diagnosis   TEXT,
  detail      TEXT,
  occurred_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_sys_time ON events(system_id, occurred_at);
`
	_, err := db.Exec(schema)
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
		// Queue full, log warning and discard to prevent slowing checkers
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
			// Drain remaining items before exiting
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
INSERT INTO checks (system_id, kind, ok, status_code, latency_ms, loss_pct, error_cat, error_msg, checked_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
`)
	if err != nil {
		return fmt.Errorf("prepare check stmt: %w", err)
	}
	defer stmtCheck.Close()

	stmtEvent, err := tx.Prepare(`
INSERT INTO events (system_id, kind, from_status, to_status, diagnosis, detail, occurred_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
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
			_, err := stmtCheck.Exec(
				item.sysID,
				item.kind,
				okInt,
				item.check.StatusCode,
				item.check.Latency.Milliseconds(),
				item.check.Loss,
				item.check.ErrorCat,
				item.check.Error,
				item.check.CheckedAt.UTC(),
			)
			if err != nil {
				return fmt.Errorf("insert check: %w", err)
			}
		} else if item.itemType == itemEvent && item.event != nil {
			_, err := stmtEvent.Exec(
				item.sysID,
				item.kind,
				string(item.event.FromStatus),
				string(item.event.ToStatus),
				item.event.Diagnosis,
				item.event.Detail,
				item.event.OccurredAt.UTC(),
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

// PurgeOldRecords removes records older than the configured retention window.
func (d *DB) PurgeOldRecords() error {
	now := time.Now().UTC()

	if d.retChecks > 0 {
		cutoff := now.Add(-d.retChecks)
		_, err := d.db.Exec("DELETE FROM checks WHERE checked_at < ?", cutoff)
		if err != nil {
			return fmt.Errorf("purge checks: %w", err)
		}
	}

	if d.retEvents > 0 {
		cutoff := now.Add(-d.retEvents)
		_, err := d.db.Exec("DELETE FROM events WHERE occurred_at < ?", cutoff)
		if err != nil {
			return fmt.Errorf("purge events: %w", err)
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
