package cli

import "testing"

// psql needs sslrootcert=system appended to verify-full URLs (libpq does not
// read the OS trust store by default); everything else passes through untouched.
func TestPsqlConnectionURL(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{
			"verify-full gets system roots",
			"postgres://u:p@h:5432/db?sslmode=verify-full",
			"postgres://u:p@h:5432/db?sslmode=verify-full&sslrootcert=system",
		},
		{
			"verify-ca gets system roots",
			"postgres://u:p@h:5432/db?sslmode=verify-ca",
			"postgres://u:p@h:5432/db?sslmode=verify-ca&sslrootcert=system",
		},
		{
			"existing sslrootcert is respected",
			"postgres://u:p@h:5432/db?sslmode=verify-full&sslrootcert=/etc/ssl/ca.crt",
			"postgres://u:p@h:5432/db?sslmode=verify-full&sslrootcert=/etc/ssl/ca.crt",
		},
		{
			"require does not verify, no append",
			"postgres://u:p@h:5432/db?sslmode=require",
			"postgres://u:p@h:5432/db?sslmode=require",
		},
		{
			"no query string stays untouched",
			"postgres://u:p@h:5432/db",
			"postgres://u:p@h:5432/db",
		},
	}
	for _, tc := range cases {
		if got := psqlConnectionURL(tc.in); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
