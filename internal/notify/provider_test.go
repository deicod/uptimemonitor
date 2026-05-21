package notify

import "testing"

// FieldType constants are exposed to the TUI via the providers metadata
// endpoint (SPEC §18.4 / §10.5) and so their string values are part of the
// wire contract — they must match the SPEC literals exactly.
func TestFieldTypeValues(t *testing.T) {
	cases := map[FieldType]string{
		FieldTypeString:       "string",
		FieldTypeSecretString: "secret_string",
		FieldTypeURL:          "url",
		FieldTypeNumber:       "number",
		FieldTypeBool:         "bool",
		FieldTypeSelect:       "select",
		FieldTypeTextarea:     "textarea",
	}
	for ft, want := range cases {
		if string(ft) != want {
			t.Errorf("FieldType %q, want %q", ft, want)
		}
	}
}
