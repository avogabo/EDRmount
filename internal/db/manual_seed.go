package db

import (
	"database/sql"
)

func seedManualRoot(db *sql.DB) {
	// Ensure root folder exists.
	_, _ = db.Exec(`INSERT OR IGNORE INTO manual_dirs(id,parent_id,name) VALUES('root','root','root')`)
}
