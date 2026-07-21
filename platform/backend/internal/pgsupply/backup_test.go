package pgsupply

import "testing"

func TestDsnForDB(t *testing.T) {
	cases := []struct {
		in, db, want string
	}{
		{"postgres://postgres:p@h:9500/postgres?sslmode=disable", "app_x", "postgres://postgres:p@h:9500/app_x?sslmode=disable"},
		{"postgres://u:p@h:5432/postgres", "app_y", "postgres://u:p@h:5432/app_y"},
	}
	for _, c := range cases {
		if got := dsnForDB(c.in, c.db); got != c.want {
			t.Fatalf("dsnForDB(%s,%s):\n got %s\nwant %s", c.in, c.db, got, c.want)
		}
	}
}
