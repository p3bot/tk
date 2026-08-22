package tkv

import (
	"errors"
	"strings"

	sqlite "modernc.org/sqlite"
)

const (
	sqliteBusy   = 5
	sqliteLocked = 6
	sqliteSchema = 17
)

func isBusy(err error) bool {
	if err == nil {
		return false
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() {
		case sqliteBusy, sqliteLocked:
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "sqlite_busy")
}

func isSchemaShaped(err error) bool {
	if err == nil {
		return false
	}
	var se *sqlite.Error
	if errors.As(err, &se) && se.Code() == sqliteSchema {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such table") ||
		strings.Contains(msg, "no such column") ||
		strings.Contains(msg, "has no column named")
}
