package auth

import "testing"

func TestJobSubjectRoundTrip(t *testing.T) {
	subject := JobSubject("4f2c1d3e-aaaa-bbbb-cccc-000000000000", "alice")
	jobID, username, ok := ParseJobSubject(subject)
	if !ok || jobID != "4f2c1d3e-aaaa-bbbb-cccc-000000000000" || username != "alice" {
		t.Fatalf("parse = %q %q %v", jobID, username, ok)
	}
	for _, plain := range []string{"alice", "", "actions-job/", "actions-job/onlyid", "actions-job//alice"} {
		if id, name, ok := ParseJobSubject(plain); ok || id != "" || name != plain {
			t.Errorf("%q should not parse as a job subject, got %q %q %v", plain, id, name, ok)
		}
	}
}
