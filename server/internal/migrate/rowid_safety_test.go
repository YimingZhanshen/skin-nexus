package migrate

import "testing"

// The rebuild feeds sqlite_master's own DDL back into the database. Nothing
// reachable today can put a foreign statement there, but the function is one
// careless caller away from executing whatever string it is handed, so it
// refuses anything that is not the table it was given.
func TestWithAutoIncrementRefusesForeignDDL(t *testing.T) {
	good := `CREATE TABLE textures (id INTEGER PRIMARY KEY, hash TEXT)`
	if out, err := withAutoIncrement(good, "textures"); err != nil {
		t.Fatalf("honest DDL rejected: %v", err)
	} else if out == good {
		t.Fatal("AUTOINCREMENT was not applied")
	}

	cases := map[string]string{
		"other table":    `CREATE TABLE users (id INTEGER PRIMARY KEY)`,
		"two statements": `CREATE TABLE textures (id INTEGER PRIMARY KEY); DROP TABLE textures`,
		"not a table":    `SELECT 1 -- INTEGER PRIMARY KEY`,
	}
	for why, ddl := range cases {
		if _, err := withAutoIncrement(ddl, "textures"); err == nil {
			t.Errorf("%s: withAutoIncrement accepted it (ddl=%q)", why, ddl)
		}
	}
}

func TestValidateSourcePrefix(t *testing.T) {
	for _, ok := range []string{"legacy_", "legacy.", "blessing_skin", "L1_."} {
		if err := ValidateSourcePrefix(ok); err != nil {
			t.Errorf("%q is a legitimate prefix: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "legacy_; DROP TABLE users", "legacy players", "a`b", "legacy." + string(make([]byte, 64))} {
		if err := ValidateSourcePrefix(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}
