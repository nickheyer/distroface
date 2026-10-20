package ghapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestArtifactRoutesWithoutActionsAnswer503(t *testing.T) {
	rec := httptest.NewRecorder()
	q := &request{h: &Handler{}, w: rec, r: httptest.NewRequest(http.MethodGet, "/repos/o/r/actions/artifacts", nil)}
	if q.actionsReady() {
		t.Fatal("nil actions manager reported ready")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
	var body errorBody
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil || !strings.Contains(body.Message, "actions are not enabled") {
		t.Fatalf("body = %+v, %v", body, err)
	}
}
