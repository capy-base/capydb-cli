package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/capy-base/capydb/cli/internal/api"
)

// diffSchemas produces a deterministic, human-readable list of structural
// differences from baseline to current ("+" added, "-" removed, "~" changed).
// It is intentionally a semantic diff over the canonical schema document, not
// a text diff: ordering, comments and cosmetic formatting never show up.
func diffSchemas(baseline, current api.DatabaseSchema) []string {
	var differences []string

	if baseline.PostgresVersion != "" && current.PostgresVersion != "" && majorVersion(baseline.PostgresVersion) != majorVersion(current.PostgresVersion) {
		differences = append(differences, fmt.Sprintf("~ postgres version: %s -> %s", baseline.PostgresVersion, current.PostgresVersion))
	}

	differences = append(differences, diffExtensions(baseline, current)...)

	baselineNamespaces := namespacesByName(baseline)
	currentNamespaces := namespacesByName(current)

	for _, name := range sortedKeys(baselineNamespaces, currentNamespaces) {
		baseNS, inBase := baselineNamespaces[name]
		currNS, inCurr := currentNamespaces[name]
		switch {
		case !inCurr:
			differences = append(differences, fmt.Sprintf("- schema %s", name))
		case !inBase:
			differences = append(differences, fmt.Sprintf("+ schema %s", name))
			for _, enum := range currNS.Enums {
				differences = append(differences, fmt.Sprintf("+ enum %s.%s (%s)", name, enum.Name, strings.Join(enum.Values, ", ")))
			}
			for _, table := range currNS.Tables {
				differences = append(differences, fmt.Sprintf("+ %s %s.%s", tableKindLabel(table.Kind), name, table.Name))
			}
		default:
			differences = append(differences, diffNamespace(name, baseNS, currNS)...)
		}
	}
	return differences
}

func diffNamespace(name string, baseline, current api.SchemaNamespace) []string {
	var differences []string

	baselineEnums := make(map[string]api.SchemaEnum, len(baseline.Enums))
	for _, enum := range baseline.Enums {
		baselineEnums[enum.Name] = enum
	}
	currentEnums := make(map[string]api.SchemaEnum, len(current.Enums))
	for _, enum := range current.Enums {
		currentEnums[enum.Name] = enum
	}
	for _, enumName := range sortedKeys(baselineEnums, currentEnums) {
		baseEnum, inBase := baselineEnums[enumName]
		currEnum, inCurr := currentEnums[enumName]
		qualified := name + "." + enumName
		switch {
		case !inCurr:
			differences = append(differences, fmt.Sprintf("- enum %s", qualified))
		case !inBase:
			differences = append(differences, fmt.Sprintf("+ enum %s (%s)", qualified, strings.Join(currEnum.Values, ", ")))
		case strings.Join(baseEnum.Values, "\x00") != strings.Join(currEnum.Values, "\x00"):
			differences = append(differences, fmt.Sprintf("~ enum %s: (%s) -> (%s)", qualified, strings.Join(baseEnum.Values, ", "), strings.Join(currEnum.Values, ", ")))
		}
	}

	baselineTables := make(map[string]api.SchemaTable, len(baseline.Tables))
	for _, table := range baseline.Tables {
		baselineTables[table.Name] = table
	}
	currentTables := make(map[string]api.SchemaTable, len(current.Tables))
	for _, table := range current.Tables {
		currentTables[table.Name] = table
	}
	for _, tableName := range sortedKeys(baselineTables, currentTables) {
		baseTable, inBase := baselineTables[tableName]
		currTable, inCurr := currentTables[tableName]
		qualified := name + "." + tableName
		switch {
		case !inCurr:
			differences = append(differences, fmt.Sprintf("- %s %s", tableKindLabel(baseTable.Kind), qualified))
		case !inBase:
			differences = append(differences, fmt.Sprintf("+ %s %s", tableKindLabel(currTable.Kind), qualified))
		default:
			differences = append(differences, diffTable(qualified, baseTable, currTable)...)
		}
	}
	return differences
}

func diffTable(qualified string, baseline, current api.SchemaTable) []string {
	var differences []string

	if baseline.Kind != current.Kind {
		differences = append(differences, fmt.Sprintf("~ %s: kind %s -> %s", qualified, baseline.Kind, current.Kind))
	}

	baselineColumns := make(map[string]api.SchemaColumn, len(baseline.Columns))
	for _, column := range baseline.Columns {
		baselineColumns[column.Name] = column
	}
	currentColumns := make(map[string]api.SchemaColumn, len(current.Columns))
	for _, column := range current.Columns {
		currentColumns[column.Name] = column
	}
	for _, columnName := range sortedKeys(baselineColumns, currentColumns) {
		baseColumn, inBase := baselineColumns[columnName]
		currColumn, inCurr := currentColumns[columnName]
		qualifiedColumn := qualified + "." + columnName
		switch {
		case !inCurr:
			differences = append(differences, fmt.Sprintf("- column %s (%s)", qualifiedColumn, baseColumn.DataType))
		case !inBase:
			differences = append(differences, fmt.Sprintf("+ column %s (%s)", qualifiedColumn, describeColumn(currColumn)))
		default:
			if change := diffColumn(baseColumn, currColumn); change != "" {
				differences = append(differences, fmt.Sprintf("~ column %s: %s", qualifiedColumn, change))
			}
		}
	}

	if strings.Join(baseline.PrimaryKey, ",") != strings.Join(current.PrimaryKey, ",") {
		differences = append(differences, fmt.Sprintf("~ %s: primary key (%s) -> (%s)", qualified, strings.Join(baseline.PrimaryKey, ", "), strings.Join(current.PrimaryKey, ", ")))
	}

	differences = append(differences, diffNamedColumnSets(qualified, "unique", uniqueSets(baseline), uniqueSets(current))...)
	differences = append(differences, diffNamedColumnSets(qualified, "foreign key", foreignKeySets(baseline), foreignKeySets(current))...)
	return differences
}

func diffColumn(baseline, current api.SchemaColumn) string {
	var changes []string
	if baseline.DataType != current.DataType {
		changes = append(changes, fmt.Sprintf("type %s -> %s", baseline.DataType, current.DataType))
	}
	if baseline.IsNullable != current.IsNullable {
		changes = append(changes, fmt.Sprintf("nullable %t -> %t", baseline.IsNullable, current.IsNullable))
	}
	if baseline.Default != current.Default {
		changes = append(changes, fmt.Sprintf("default %s -> %s", orNone(baseline.Default), orNone(current.Default)))
	}
	if baseline.Identity != current.Identity {
		changes = append(changes, fmt.Sprintf("identity %s -> %s", orNone(baseline.Identity), orNone(current.Identity)))
	}
	if baseline.IsGenerated != current.IsGenerated {
		changes = append(changes, fmt.Sprintf("generated %t -> %t", baseline.IsGenerated, current.IsGenerated))
	}
	if baseline.ArrayDims != current.ArrayDims {
		changes = append(changes, fmt.Sprintf("array dimensions %d -> %d", baseline.ArrayDims, current.ArrayDims))
	}
	return strings.Join(changes, ", ")
}

// diffNamedColumnSets compares name -> description maps (unique constraints,
// foreign keys) and reports additions, removals and definition changes.
func diffNamedColumnSets(qualified, kind string, baseline, current map[string]string) []string {
	var differences []string
	for _, name := range sortedKeys(baseline, current) {
		baseDef, inBase := baseline[name]
		currDef, inCurr := current[name]
		switch {
		case !inCurr:
			differences = append(differences, fmt.Sprintf("- %s %s on %s (%s)", kind, name, qualified, baseDef))
		case !inBase:
			differences = append(differences, fmt.Sprintf("+ %s %s on %s (%s)", kind, name, qualified, currDef))
		case baseDef != currDef:
			differences = append(differences, fmt.Sprintf("~ %s %s on %s: %s -> %s", kind, name, qualified, baseDef, currDef))
		}
	}
	return differences
}

func diffExtensions(baseline, current api.DatabaseSchema) []string {
	baselineExtensions := make(map[string]string, len(baseline.Extensions))
	for _, extension := range baseline.Extensions {
		baselineExtensions[extension.Name] = extension.Version
	}
	currentExtensions := make(map[string]string, len(current.Extensions))
	for _, extension := range current.Extensions {
		currentExtensions[extension.Name] = extension.Version
	}

	var differences []string
	for _, name := range sortedKeys(baselineExtensions, currentExtensions) {
		_, inBase := baselineExtensions[name]
		_, inCurr := currentExtensions[name]
		switch {
		case !inCurr:
			differences = append(differences, fmt.Sprintf("- extension %s", name))
		case !inBase:
			differences = append(differences, fmt.Sprintf("+ extension %s", name))
		}
		// Version-only changes are omitted on purpose: extension versions move
		// with platform maintenance, not with the application's schema.
	}
	return differences
}

func uniqueSets(table api.SchemaTable) map[string]string {
	sets := make(map[string]string, len(table.UniqueConstraints))
	for _, unique := range table.UniqueConstraints {
		sets[unique.Name] = strings.Join(unique.Columns, ", ")
	}
	return sets
}

func foreignKeySets(table api.SchemaTable) map[string]string {
	sets := make(map[string]string, len(table.ForeignKeys))
	for _, fk := range table.ForeignKeys {
		sets[fk.Name] = fmt.Sprintf("(%s) -> %s.%s(%s)", strings.Join(fk.Columns, ", "), fk.ReferencedSchema, fk.ReferencedTable, strings.Join(fk.ReferencedColumns, ", "))
	}
	return sets
}

func namespacesByName(schema api.DatabaseSchema) map[string]api.SchemaNamespace {
	namespaces := make(map[string]api.SchemaNamespace, len(schema.Schemas))
	for _, namespace := range schema.Schemas {
		namespaces[namespace.Name] = namespace
	}
	return namespaces
}

// sortedKeys returns the union of both maps' keys, sorted.
func sortedKeys[V any](a, b map[string]V) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	keys := make([]string, 0, len(a)+len(b))
	for key := range a {
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for key := range b {
		if _, ok := seen[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func tableKindLabel(kind string) string {
	switch kind {
	case "view":
		return "view"
	case "materialized_view":
		return "materialized view"
	case "foreign_table":
		return "foreign table"
	default:
		return "table"
	}
}

// describeColumn renders a compact one-line column definition for "+ column"
// diff lines.
func describeColumn(column api.SchemaColumn) string {
	description := column.DataType
	if !column.IsNullable {
		description += " not null"
	}
	switch column.Identity {
	case "always":
		description += " generated always as identity"
	case "by_default":
		description += " generated by default as identity"
	}
	if column.IsGenerated {
		description += " generated"
	}
	if column.Default != "" && column.Identity == "" && !column.IsGenerated {
		description += " default " + column.Default
	}
	return description
}

func orNone(value string) string {
	if value == "" {
		return "(none)"
	}
	return value
}

func majorVersion(version string) string {
	if index := strings.IndexAny(version, ". "); index > 0 {
		return version[:index]
	}
	return version
}
