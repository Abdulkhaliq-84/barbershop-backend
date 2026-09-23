package config

import (
	"reflect"
	"testing"
)

// withEnvNames maps errors by Go field name, so two fields with the same name
// in different nested structs would be ambiguous. Guard it.
func TestEnvKeysAreUnique(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool)
	var walk func(reflect.Type)
	walk = func(t2 reflect.Type) {
		for i := range t2.NumField() {
			f := t2.Field(i)
			if f.Type.Kind() == reflect.Struct && f.Tag.Get("env") == "" {
				walk(f.Type)
				continue
			}
			if seen[f.Name] {
				t.Errorf("field name %q is used twice in Config; rename one", f.Name)
			}
			seen[f.Name] = true
		}
	}
	walk(reflect.TypeFor[Config]())

	if got := envKeys(reflect.TypeFor[Config]())["ReadTimeout"]; got != "HTTP_READ_TIMEOUT" {
		t.Errorf(`envKeys()["ReadTimeout"] = %q, want HTTP_READ_TIMEOUT`, got)
	}
}
