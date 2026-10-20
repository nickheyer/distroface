package migrations

import (
	"github.com/nickheyer/distroface/pkg/logger"
	"gorm.io/gorm"
)

func init() {
	register(migration{
		id:      "202610150001",
		name:    "drop_stopwatches",
		migrate: dropStopwatches,
	})
}

// Live timers are gone, time comes from the issue timeline
func dropStopwatches(tx *gorm.DB, _ *logger.Logger) error {
	if !tx.Migrator().HasTable("git_stopwatches") {
		return nil
	}
	return tx.Migrator().DropTable("git_stopwatches")
}
