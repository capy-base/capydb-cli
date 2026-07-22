package cli

import (
	"strings"
	"testing"

	"github.com/capy-base/capydb/cli/internal/api"
)

func testSchema() api.DatabaseSchema {
	return api.DatabaseSchema{
		DatabaseName:    "app",
		PostgresVersion: "17.5",
		Extensions:      []api.SchemaExtension{{Name: "pgcrypto", Version: "1.3"}},
		Schemas: []api.SchemaNamespace{{
			Name:  "public",
			Enums: []api.SchemaEnum{{Name: "status", Values: []string{"a", "b"}}},
			Tables: []api.SchemaTable{{
				Name: "users",
				Kind: "table",
				Columns: []api.SchemaColumn{
					{Name: "id", DataType: "bigint", UDTName: "int8", Identity: "always"},
					{Name: "email", DataType: "text", UDTName: "text"},
				},
				PrimaryKey:        []string{"id"},
				UniqueConstraints: []api.SchemaUniqueConstraint{{Name: "users_email_key", Columns: []string{"email"}}},
			}},
		}},
	}
}

func TestDiffSchemasNoChanges(t *testing.T) {
	if differences := diffSchemas(testSchema(), testSchema()); len(differences) != 0 {
		t.Errorf("identical schemas must produce no differences, got %v", differences)
	}
}

func TestDiffSchemasDetectsChanges(t *testing.T) {
	baseline := testSchema()
	current := testSchema()

	// Column type change + new column + dropped unique + new table + enum change.
	current.Schemas[0].Tables[0].Columns[1].DataType = "character varying(255)"
	current.Schemas[0].Tables[0].Columns = append(current.Schemas[0].Tables[0].Columns, api.SchemaColumn{
		Name: "created_at", DataType: "timestamp with time zone", UDTName: "timestamptz", Default: "now()",
	})
	current.Schemas[0].Tables[0].UniqueConstraints = nil
	current.Schemas[0].Tables = append(current.Schemas[0].Tables, api.SchemaTable{Name: "orders", Kind: "table"})
	current.Schemas[0].Enums[0].Values = []string{"a", "b", "c"}
	current.Extensions = append(current.Extensions, api.SchemaExtension{Name: "vector", Version: "0.8.0"})

	differences := diffSchemas(baseline, current)
	joined := strings.Join(differences, "\n")

	for _, want := range []string{
		"~ column public.users.email: type text -> character varying(255)",
		"+ column public.users.created_at (timestamp with time zone not null default now())",
		"- unique users_email_key on public.users (email)",
		"+ table public.orders",
		"~ enum public.status: (a, b) -> (a, b, c)",
		"+ extension vector",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("diff missing %q, got:\n%s", want, joined)
		}
	}
}

func TestDiffSchemasNullableAndDirectionality(t *testing.T) {
	baseline := testSchema()
	current := testSchema()
	current.Schemas[0].Tables[0].Columns[1].IsNullable = true

	differences := diffSchemas(baseline, current)
	if len(differences) != 1 || !strings.Contains(differences[0], "nullable false -> true") {
		t.Errorf("expected single nullable change, got %v", differences)
	}

	// Reversed direction flips the sign.
	reversed := diffSchemas(current, baseline)
	if len(reversed) != 1 || !strings.Contains(reversed[0], "nullable true -> false") {
		t.Errorf("expected reversed nullable change, got %v", reversed)
	}
}

func TestDiffSchemasArrayDimensions(t *testing.T) {
	baseline := testSchema()
	current := testSchema()
	baseline.Schemas[0].Tables[0].Columns[1].IsArray = true
	baseline.Schemas[0].Tables[0].Columns[1].ArrayDims = 1
	current.Schemas[0].Tables[0].Columns[1].IsArray = true
	current.Schemas[0].Tables[0].Columns[1].ArrayDims = 2

	differences := diffSchemas(baseline, current)
	if len(differences) != 1 || !strings.Contains(differences[0], "array dimensions 1 -> 2") {
		t.Errorf("expected array-dimension change, got %v", differences)
	}
}
