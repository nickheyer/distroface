package actions

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Key purpose behind the results api bearer tokens
const runtimeTokenPurpose = "actions-runtime"

// Claims the actions toolkit decodes out of ACTIONS_RUNTIME_TOKEN
type runtimeClaims struct {
	jwt.RegisteredClaims
	Scope string `json:"scp"`
	JobID string `json:"job_id"`
	RunID string `json:"run_id"`
}

// Mints the bearer a job presents to the results and cache apis
func (m *Manager) runtimeToken(jobID, runID string, timeoutMinutes int) (string, error) {
	now := time.Now()
	claims := runtimeClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(timeoutMinutes)*time.Minute + time.Hour)),
			NotBefore: jwt.NewNumericDate(now.Add(-time.Minute)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
		Scope: fmt.Sprintf("Actions.Results:%s:%s", runID, jobID),
		JobID: jobID,
		RunID: runID,
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.auth.DerivedKey(runtimeTokenPurpose))
}

// ParseRuntimeToken answers the job and run a bearer names
func (m *Manager) ParseRuntimeToken(token string) (jobID, runID string, err error) {
	parsed, err := jwt.ParseWithClaims(token, &runtimeClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return m.auth.DerivedKey(runtimeTokenPurpose), nil
	})
	if err != nil {
		return "", "", err
	}
	claims, ok := parsed.Claims.(*runtimeClaims)
	if !ok || !parsed.Valid || claims.JobID == "" {
		return "", "", errors.New("invalid runtime token")
	}
	return claims.JobID, claims.RunID, nil
}
