// Runs versioned data migrations after auto migrate
// Auto inits files named <id>_<name>.go
// Ids are 12 digit YYYYMMDDNNNN
package migrations

import (
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/go-gormigrate/gormigrate/v2"
	"github.com/nickheyer/distroface/pkg/logger"
	"gorm.io/gorm"
)

type migrateFunc func(tx *gorm.DB, log *logger.Logger) error

type migration struct {
	id       string
	name     string
	migrate  migrateFunc
	rollback migrateFunc
}

var registry []migration

var idPattern = regexp.MustCompile(`^\d{12}$`)

// Called from init in each migration file
func register(m migration) {
	registry = append(registry, m)
}

// Signature kept stable for store callers without a logger
func Run(gdb *gorm.DB) error {
	return RunWithLogger(gdb, logger.New().Module("migrations"))
}

// Applies pending migrations in id order inside transactions
func RunWithLogger(gdb *gorm.DB, log *logger.Logger) error {
	ms, err := ordered()
	if err != nil {
		return err
	}
	if len(ms) == 0 {
		log.Info("no migrations registered")
		return nil
	}

	applied := 0
	gms := make([]*gormigrate.Migration, len(ms))
	for i, m := range ms {
		gms[i] = m.wrap(log, &applied)
	}

	log.Info("checking %d registered migrations", len(ms))
	if err := gormigrate.New(gdb, options(), gms).Migrate(); err != nil {
		return fmt.Errorf("migration failed after %d applied: %w", applied, err)
	}
	if applied == 0 {
		log.Info("database up to date")
	} else {
		log.Info("applied %d migrations", applied)
	}
	return nil
}

// Reverts the newest applied migration if it defines rollback
func RollbackLast(gdb *gorm.DB, log *logger.Logger) error {
	ms, err := ordered()
	if err != nil {
		return err
	}
	if len(ms) == 0 {
		log.Info("no migrations registered")
		return nil
	}

	applied := 0
	gms := make([]*gormigrate.Migration, len(ms))
	for i, m := range ms {
		gms[i] = m.wrap(log, &applied)
	}
	return gormigrate.New(gdb, options(), gms).RollbackLast()
}

func options() *gormigrate.Options {
	opts := *gormigrate.DefaultOptions
	opts.UseTransaction = true
	return &opts
}

// Validates entries then sorts so run order is deterministic
func ordered() ([]migration, error) {
	seen := make(map[string]bool, len(registry))
	for _, m := range registry {
		if !idPattern.MatchString(m.id) {
			return nil, fmt.Errorf("migration id %q must be 12 digits", m.id)
		}
		if m.name == "" {
			return nil, fmt.Errorf("migration %s has no name", m.id)
		}
		if m.migrate == nil {
			return nil, fmt.Errorf("migration %s has no migrate func", m.id)
		}
		if seen[m.id] {
			return nil, fmt.Errorf("duplicate migration id %q", m.id)
		}
		seen[m.id] = true
	}
	out := append([]migration(nil), registry...)
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out, nil
}

// Adds timing and outcome logs around gormigrate execution
func (m migration) wrap(log *logger.Logger, applied *int) *gormigrate.Migration {
	g := &gormigrate.Migration{
		ID: m.id,
		Migrate: func(tx *gorm.DB) error {
			log.Info("applying %s %s", m.id, m.name)
			start := time.Now()
			if err := m.migrate(tx, log); err != nil {
				log.Error("migration %s failed: %v", m.id, err)
				return err
			}
			*applied++
			log.Info("migration %s done in %s", m.id, time.Since(start).Round(time.Millisecond))
			return nil
		},
	}
	if m.rollback != nil {
		g.Rollback = func(tx *gorm.DB) error {
			log.Warn("rolling back %s %s", m.id, m.name)
			if err := m.rollback(tx, log); err != nil {
				log.Error("rollback %s failed: %v", m.id, err)
				return err
			}
			log.Info("rolled back %s", m.id)
			return nil
		}
	}
	return g
}
