package domain

import (
	"encoding/json"
	"os"
	"testing"
)

func TestStateSchemaVocabulary(t *testing.T) {
	data, err := os.ReadFile("../../schemas/state.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err = json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	for _, value := range schema.Properties["run_status"].Enum {
		if !RunStatus(value).Valid() {
			t.Fatalf("schema run status absent in domain: %s", value)
		}
	}
	for _, value := range schema.Properties["task_status"].Enum {
		if !TaskStatus(value).Valid() {
			t.Fatalf("schema task status absent in domain: %s", value)
		}
	}
	if len(schema.Properties["run_status"].Enum) != 11 || len(schema.Properties["task_status"].Enum) != 10 {
		t.Fatal("vocabulary changed; reconcile API/schema/domain contracts")
	}
}
