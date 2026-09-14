package common

// HasUnsupportedUnicodePropertyEscape reports whether a regular expression string
// contains unescaped Unicode property escape sequences (\p{...} or \P{...}).
// Python's built-in re module (and schema validators relying on it) fails compilation
// with "bad escape \p" on these sequences.
func HasUnsupportedUnicodePropertyEscape(pattern string) bool {
	for i := 0; i < len(pattern); i++ {
		if pattern[i] != '\\' {
			continue
		}
		if i+2 < len(pattern) &&
			(pattern[i+1] == 'p' || pattern[i+1] == 'P') &&
			pattern[i+2] == '{' {
			return true
		}
		i++ // skip the escaped character (including escaped backslash)
	}
	return false
}

// SchemaMapKeywords lists JSON Schema keywords whose values are maps of subschemas.
var SchemaMapKeywords = [...]string{
	"properties",
	"$defs",
	"definitions",
	"patternProperties",
	"dependentSchemas",
	"dependencies",
}

// SchemaValueKeywords lists JSON Schema keywords with a single nested subschema or a slice of subschemas.
var SchemaValueKeywords = [...]string{
	"items",
	"prefixItems",
	"contains",
	"additionalProperties",
	"propertyNames",
	"unevaluatedProperties",
	"unevaluatedItems",
	"additionalItems",
	"contentSchema",
	"anyOf",
	"oneOf",
	"allOf",
	"not",
	"if",
	"then",
	"else",
}
