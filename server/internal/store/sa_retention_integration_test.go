//go:build integration

package store

import "testing"

// Run the retention DELETE and policy query on every SKIN_TEST_DSN engine in
// CI, not only on the default-build SQLite fixture.
func TestIntegrationSAEventRetentionDeletesOnlyExpiredServerEventsInBatches(t *testing.T) {
	st := openTestStore(t)
	saExerciseEventRetentionDeletesOnlyExpiredServerEventsInBatches(t, st)
}

func TestIntegrationSAEventRetentionPoliciesIncludeDisabledServersButNotDeletedTenants(t *testing.T) {
	st := openTestStore(t)
	saExerciseEventRetentionPoliciesIncludeDisabledServersButNotDeletedTenants(t, st)
}
