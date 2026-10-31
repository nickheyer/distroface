package stores

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// Last schema pass fingerprint, lets restarts skip it
type schemaState struct {
	ID          int    `gorm:"primaryKey"`
	Fingerprint string `gorm:"not null"`
	UpdatedAt   time.Time
}

func (schemaState) TableName() string { return "schema_states" }

// Runs automigrate unless the stored fingerprint still matches
func (s *Store) autoMigrate() (bool, error) {
	if err := s.db.AutoMigrate(&schemaState{}); err != nil {
		return false, fmt.Errorf("failed to migrate schema state: %w", err)
	}
	fingerprint, err := s.schemaFingerprint(schemaModels)
	if err != nil {
		// A fingerprint bug must never hold the schema back
		fingerprint = ""
	}
	var state schemaState
	err = s.db.First(&state, "id = ?", 1).Error
	if err == nil && fingerprint != "" && state.Fingerprint == fingerprint {
		return false, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, fmt.Errorf("failed to read schema state: %w", err)
	}
	if err := s.db.AutoMigrate(schemaModels...); err != nil {
		return true, fmt.Errorf("failed to auto-migrate: %w", err)
	}
	if fingerprint == "" {
		return true, nil
	}
	state = schemaState{ID: 1, Fingerprint: fingerprint, UpdatedAt: time.Now().UTC()}
	if err := s.db.Save(&state).Error; err != nil {
		return true, fmt.Errorf("failed to record schema state: %w", err)
	}
	return true, nil
}

// Hashes everything automigrate compares against the live schema
func (s *Store) schemaFingerprint(models []any) (string, error) {
	h := sha256.New()
	write := func(parts ...string) {
		for _, p := range parts {
			h.Write([]byte(p))
			h.Write([]byte{0})
		}
		h.Write([]byte{'\n'})
	}
	write("driver", string(s.driver))
	write("modules", moduleVersions("gorm.io/gorm", "gorm.io/driver/sqlite", "gorm.io/driver/postgres"))
	migrator := s.db.Migrator()
	for _, model := range models {
		stmt := &gorm.Statement{DB: s.db}
		if err := stmt.Parse(model); err != nil {
			return "", err
		}
		sch := stmt.Schema
		write("table", sch.Table)
		for _, f := range sch.Fields {
			if f.DBName == "" || f.IgnoreMigration {
				continue
			}
			full := migrator.FullDataTypeOf(f)
			write("column", f.DBName, full.SQL, fmt.Sprint(full.Vars...),
				fmt.Sprint(f.PrimaryKey, f.AutoIncrement, f.NotNull, f.Unique, f.HasDefaultValue),
				f.DefaultValue, f.Comment)
		}
		indexes := sch.ParseIndexes()
		sort.Slice(indexes, func(i, j int) bool { return indexes[i].Name < indexes[j].Name })
		for _, idx := range indexes {
			cols := make([]string, 0, len(idx.Fields))
			for _, opt := range idx.Fields {
				name := ""
				if opt.Field != nil {
					name = opt.DBName
				}
				cols = append(cols, fmt.Sprint(name, " ", opt.Expression, " ", opt.Sort, " ", opt.Collate, " ", opt.Length, " ", opt.Priority))
			}
			write("index", idx.Name, idx.Class, idx.Type, idx.Where, idx.Comment, idx.Option, strings.Join(cols, ","))
		}
		write("constraints", constraintSummary(sch))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func constraintSummary(sch *schema.Schema) string {
	names := make([]string, 0, len(sch.Relationships.Relations))
	for name := range sch.Relationships.Relations {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		c := sch.Relationships.Relations[name].ParseConstraint()
		if c == nil {
			continue
		}
		ref := ""
		if c.ReferenceSchema != nil {
			ref = c.ReferenceSchema.Table
		}
		parts = append(parts, strings.Join([]string{c.Name, c.OnDelete, c.OnUpdate, fieldNames(c.ForeignKeys), ref, fieldNames(c.References)}, " "))
	}
	return strings.Join(parts, "|")
}

func fieldNames(fields []*schema.Field) string {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.DBName
	}
	return strings.Join(names, ",")
}

// Type mapping lives in these modules so their versions count
func moduleVersions(paths ...string) string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	var parts []string
	for _, dep := range info.Deps {
		for _, p := range paths {
			if dep.Path == p {
				parts = append(parts, dep.Path+"@"+dep.Version)
			}
		}
	}
	return strings.Join(parts, ",")
}
