package memex

import (
	"embed"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var MigrationsFS embed.FS

// MigrationSQL returns all migration SQL concatenated in filename order.
func MigrationSQL() string {
	entries, err := MigrationsFS.ReadDir("migrations")
	if err != nil {
		panic("memex: missing embedded migrations: " + err.Error())
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var b strings.Builder
	for _, name := range names {
		data, err := MigrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			panic("memex: read migration " + name + ": " + err.Error())
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String()
}
