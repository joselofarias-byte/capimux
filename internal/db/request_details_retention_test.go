package db

import (
	"testing"
	"time"
)

func insertExpiredRequestDetail(t *testing.T, r *Repo, id string) {
	t.Helper()
	old := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02T15:04:05.000Z")
	if _, err := r.db.Exec(`INSERT INTO requestDetails (id, timestamp, provider, model, connectionId, status, data)
		VALUES (?, ?, 'p', 'm', 'c', 'success', '{}')`, id, old); err != nil {
		t.Fatalf("insert expired row: %v", err)
	}
}

func countRequestDetailRows(t *testing.T, r *Repo, id string) int {
	t.Helper()
	var n int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM requestDetails WHERE id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A failed retention cleanup must not turn a stored record into a reported
// insert failure: the caller would log a lost diagnostic that is in fact saved.
func TestInsertRequestDetailPurgeFailureDoesNotFailTheInsert(t *testing.T) {
	t.Setenv("CAPIMUX_REQUEST_DETAILS_RETENTION_DAYS", "7")
	r := openEphemeralSchemaDB(t)
	if _, err := r.db.Exec(`CREATE TRIGGER rd_no_delete BEFORE DELETE ON requestDetails
		BEGIN SELECT RAISE(ABORT, 'delete blocked by test'); END`); err != nil {
		t.Fatal(err)
	}
	insertExpiredRequestDetail(t, r, "rd-expired") // gives the purge a row to delete
	requestDetailsInsertCounter.Store(0)           // next insert runs the sampled purge
	if err := r.InsertRequestDetail("rd-kept", "p", "m", "c", "success", `{"id":"rd-kept"}`); err != nil {
		t.Fatalf("insert reported failure although the row was stored: %v", err)
	}
	if got := countRequestDetailRows(t, r, "rd-kept"); got != 1 {
		t.Fatalf("row count = %d, want 1", got)
	}
}

// Reads hide expired rows every time but delete them at most once per
// requestDetailsReadPurgeInterval, so the dashboard does not DELETE on every poll.
func TestGetRequestDetailsPagedFiltersAlwaysAndPurgesAtMostPerInterval(t *testing.T) {
	t.Setenv("CAPIMUX_REQUEST_DETAILS_RETENTION_DAYS", "7")
	r := openEphemeralSchemaDB(t)
	t.Cleanup(func() { requestDetailsLastReadPurge.Store(0) })

	requestDetailsLastReadPurge.Store(time.Now().UnixNano()) // a purge just ran
	insertExpiredRequestDetail(t, r, "rd-expired")
	values, total, err := r.GetRequestDetailsPaged(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(values) != 0 {
		t.Fatalf("expired row visible: total=%d values=%v", total, values)
	}
	if got := countRequestDetailRows(t, r, "rd-expired"); got != 1 {
		t.Fatalf("read purged inside the interval: rows=%d, want 1", got)
	}

	requestDetailsLastReadPurge.Store(time.Now().Add(-requestDetailsReadPurgeInterval).UnixNano())
	if _, _, err := r.GetRequestDetailsPaged(10, 0); err != nil {
		t.Fatal(err)
	}
	if got := countRequestDetailRows(t, r, "rd-expired"); got != 0 {
		t.Fatalf("read did not purge after the interval: rows=%d", got)
	}
}
