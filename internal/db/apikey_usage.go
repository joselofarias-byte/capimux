package db

import "fmt"

// ApiKeyUsageBucket is the token and request count of one client API key in
// one UTC hour (hour = unix seconds / 3600).
type ApiKeyUsageBucket struct {
	KeyID    string
	Hour     int64
	Tokens   int64
	Requests int64
}

// apiKeyUsageSchema is additive and reversible: rolling back is
// `DROP TABLE apiKeyUsageHourly;`. Nothing else references it.
const apiKeyUsageSchema = `CREATE TABLE IF NOT EXISTS apiKeyUsageHourly (
	apiKeyId TEXT NOT NULL,
	hour INTEGER NOT NULL,
	tokens INTEGER NOT NULL DEFAULT 0,
	requests INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (apiKeyId, hour)
)`

// EnsureApiKeyUsageSchema creates the per-key hourly usage table if missing.
func (r *Repo) EnsureApiKeyUsageSchema() error {
	if _, err := r.db.Exec(apiKeyUsageSchema); err != nil {
		return fmt.Errorf("api key usage schema: %w", err)
	}
	return nil
}

// AddApiKeyUsage adds tokens and requests to a key's hourly bucket in a single
// UPSERT, so concurrent writers never lose an increment.
func (r *Repo) AddApiKeyUsage(keyID string, hour, tokens, requests int64) error {
	_, err := r.db.Exec(
		`INSERT INTO apiKeyUsageHourly (apiKeyId, hour, tokens, requests) VALUES (?, ?, ?, ?)
		 ON CONFLICT(apiKeyId, hour) DO UPDATE SET tokens = tokens + excluded.tokens, requests = requests + excluded.requests`,
		keyID, hour, tokens, requests,
	)
	if err != nil {
		return fmt.Errorf("add api key usage %s: %w", keyID, err)
	}
	return nil
}

// GetApiKeyUsageBuckets returns every bucket at or after sinceHour.
func (r *Repo) GetApiKeyUsageBuckets(sinceHour int64) ([]ApiKeyUsageBucket, error) {
	rows, err := r.db.Query(
		`SELECT apiKeyId, hour, tokens, requests FROM apiKeyUsageHourly WHERE hour >= ? ORDER BY hour`, sinceHour)
	if err != nil {
		return nil, fmt.Errorf("get api key usage: %w", err)
	}
	defer rows.Close()
	var out []ApiKeyUsageBucket
	for rows.Next() {
		var b ApiKeyUsageBucket
		if err := rows.Scan(&b.KeyID, &b.Hour, &b.Tokens, &b.Requests); err != nil {
			return nil, fmt.Errorf("scan api key usage: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// PruneApiKeyUsage deletes buckets older than beforeHour.
func (r *Repo) PruneApiKeyUsage(beforeHour int64) error {
	if _, err := r.db.Exec(`DELETE FROM apiKeyUsageHourly WHERE hour < ?`, beforeHour); err != nil {
		return fmt.Errorf("prune api key usage: %w", err)
	}
	return nil
}
