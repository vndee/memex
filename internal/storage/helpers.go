package storage

import (
	"database/sql"
	"time"
)

// nowRFC3339 returns the current UTC time formatted as RFC3339Nano.
func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// parseTime parses an RFC3339Nano string into a time.Time.
func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// parseTimePtr parses an RFC3339Nano NullString into a *time.Time.
// Returns nil when ns is not valid.
func parseTimePtr(ns sql.NullString) *time.Time {
	if !ns.Valid {
		return nil
	}
	t, _ := time.Parse(time.RFC3339Nano, ns.String)
	return &t
}

// nilIfEmpty returns nil for empty strings, making the DB column NULL.
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
