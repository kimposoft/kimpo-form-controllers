package linkedform

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestLinkedFormLocalesHaveFourLanguageParityWithoutRetiredQueryTerms(t *testing.T) {
	expectedLocales := []string{"en-US.json", "ja-JP.json", "zh-CN.json", "zh-TW.json"}
	entries, err := os.ReadDir("locales")
	if err != nil {
		t.Fatal(err)
	}
	var actualLocales []string
	localeMessages := make(map[string]map[string]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		actualLocales = append(actualLocales, entry.Name())
		raw, err := os.ReadFile(filepath.Join("locales", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var messages map[string]string
		if err := json.Unmarshal(raw, &messages); err != nil {
			t.Fatalf("decode %s: %v", entry.Name(), err)
		}
		localeMessages[entry.Name()] = messages
	}
	sort.Strings(actualLocales)
	if !reflect.DeepEqual(actualLocales, expectedLocales) {
		t.Fatalf("linked-form locales = %v, want %v", actualLocales, expectedLocales)
	}

	var expectedKeys []string
	for key := range localeMessages["en-US.json"] {
		expectedKeys = append(expectedKeys, key)
	}
	sort.Strings(expectedKeys)
	for _, locale := range expectedLocales {
		messages := localeMessages[locale]
		var keys []string
		for key, message := range messages {
			keys = append(keys, key)
			lower := strings.ToLower(message)
			if strings.Contains(lower, "host.query") || strings.Contains(lower, "query handle") || strings.Contains(lower, "query 句柄") {
				t.Errorf("%s key %s retains a retired Query-handle term", locale, key)
			}
		}
		sort.Strings(keys)
		if !reflect.DeepEqual(keys, expectedKeys) {
			t.Errorf("%s keys = %v, want %v", locale, keys, expectedKeys)
		}
		if strings.TrimSpace(messages["linked_form.no_host"]) == "" {
			t.Errorf("%s linked_form.no_host must be translated", locale)
		}
		localeName := strings.TrimSuffix(locale, filepath.Ext(locale))
		if got := tr.T(localeName, "linked_form.no_host", nil); got != messages["linked_form.no_host"] {
			t.Errorf("embedded %s linked_form.no_host = %q, want %q", locale, got, messages["linked_form.no_host"])
		}
	}
}
