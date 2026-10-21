package router

import (
	"context"
	"testing"

	"github.com/kimposoft/kimpo-plugin-sdk/gen/bizpb"
)

func TestGetActionsSortWeights(t *testing.T) {
	resp, err := New().GetActions(context.Background(), &bizpb.GetActionsRequest{})
	if err != nil {
		t.Fatalf("GetActions() error = %v", err)
	}
	// 2026-07-24 合并：ui.fill_form(70)/ui.open_form(80) → ui.linked_form(70)。
	want := map[string]int32{"ui.linked_form": 70, "ui.form_control": 90}
	for _, action := range resp.GetActions() {
		weight, ok := want[action.GetName()]
		if !ok {
			t.Fatalf("unexpected action %q", action.GetName())
		}
		if action.GetSortWeight() != weight {
			t.Errorf("action %q sort weight = %d, want %d", action.GetName(), action.GetSortWeight(), weight)
		}
		delete(want, action.GetName())
	}
	for name := range want {
		t.Errorf("missing action %q", name)
	}
}
