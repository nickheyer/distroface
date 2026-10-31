package stores

import (
	"path/filepath"
	"testing"
)

func TestAutoMigrateSkipsUnchangedSchema(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "fp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	var state schemaState
	if err := store.db.First(&state, "id = ?", 1).Error; err != nil {
		t.Fatalf("schema state missing after open: %v", err)
	}
	if len(state.Fingerprint) != 64 {
		t.Fatalf("fingerprint = %q", state.Fingerprint)
	}

	migrated, err := store.autoMigrate()
	if err != nil || migrated {
		t.Fatalf("unchanged schema migrated=%v err=%v", migrated, err)
	}

	if err := store.db.Model(&schemaState{}).Where("id = ?", 1).Update("fingerprint", "stale").Error; err != nil {
		t.Fatal(err)
	}
	migrated, err = store.autoMigrate()
	if err != nil || !migrated {
		t.Fatalf("stale fingerprint migrated=%v err=%v", migrated, err)
	}
	var after schemaState
	if err := store.db.First(&after, "id = ?", 1).Error; err != nil || after.Fingerprint != state.Fingerprint {
		t.Fatalf("fingerprint not restored: %q, %v", after.Fingerprint, err)
	}
}

func TestSchemaFingerprintIsStable(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "fp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	first, err := store.schemaFingerprint(schemaModels)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.schemaFingerprint(schemaModels)
	if err != nil || first != second {
		t.Fatalf("fingerprint drifted between calls: %q vs %q, %v", first, second, err)
	}
	if partial, _ := store.schemaFingerprint(schemaModels[:1]); partial == first {
		t.Fatal("fingerprint ignores the model list")
	}
}
