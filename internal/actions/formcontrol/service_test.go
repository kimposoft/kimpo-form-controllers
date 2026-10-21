package formcontrol

import (
	"context"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/kimposoft/kimpo-plugin-sdk/gen/bizpb"
)

func executeRequest(t *testing.T, op, locale string) *bizpb.ExecuteRequest {
	t.Helper()
	ctx, err := structpb.NewStruct(map[string]any{"params": map[string]any{"op": op}})
	if err != nil {
		t.Fatal(err)
	}
	return &bizpb.ExecuteRequest{
		ActionName:     ActionName,
		Context:        ctx,
		TriggerContext: &bizpb.TriggerContextMsg{Locale: locale},
	}
}

func TestExecuteMapsOperationsToSingleAwaitedCommand(t *testing.T) {
	for _, tc := range []struct {
		op      string
		command string
	}{
		{op: "save", command: "form.save"},
		{op: "close", command: "form.close"},
		{op: "edit", command: "form.edit"},
	} {
		t.Run(tc.op, func(t *testing.T) {
			resp, err := New().Execute(context.Background(), executeRequest(t, tc.op, "zh-CN"))
			if err != nil || !resp.GetSuccess() {
				t.Fatalf("Execute() response=%+v err=%v", resp, err)
			}
			effects, _ := resp.GetResult().AsMap()["effects"].([]any)
			if len(effects) != 1 {
				t.Fatalf("effects=%v, want exactly one", effects)
			}
			effect, _ := effects[0].(map[string]any)
			if effect["kind"] != "command" || effect["command"] != tc.command || effect["await"] != true {
				t.Fatalf("effect=%+v", effect)
			}
		})
	}
}

func TestExecuteBadOperationReturnsStableLocalizedFailureWithoutEffects(t *testing.T) {
	resp, err := New().Execute(context.Background(), executeRequest(t, "delete", "en-US"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetSuccess() || resp.GetErrorCode() != "FORM_CONTROL_BAD_OP" || resp.GetMessageKey() != "form_control.bad_op" {
		t.Fatalf("response=%+v", resp)
	}
	if resp.GetResult() != nil {
		t.Fatalf("failure must not emit effects: %+v", resp.GetResult())
	}
	if resp.GetMessage() != "op must be one of save, close, or edit" {
		t.Fatalf("english fallback=%q", resp.GetMessage())
	}
}

func TestValidateParamsRejectsMissingInvalidAndUnknownValues(t *testing.T) {
	for _, params := range []map[string]any{
		{},
		{"op": "delete"},
		{"op": "save", "extra": true},
	} {
		raw, _ := structpb.NewStruct(params)
		resp, err := New().ValidateParams(context.Background(), &bizpb.ValidateParamsRequest{ActionName: ActionName, Params: raw})
		if err != nil {
			t.Fatal(err)
		}
		if resp.GetValid() || len(resp.GetErrors()) == 0 {
			t.Fatalf("params=%v unexpectedly valid: %+v", params, resp)
		}
	}

	raw, _ := structpb.NewStruct(map[string]any{"op": " close "})
	resp, err := New().ValidateParams(context.Background(), &bizpb.ValidateParamsRequest{ActionName: ActionName, Params: raw})
	if err != nil || !resp.GetValid() {
		t.Fatalf("trimmed valid op rejected: %+v err=%v", resp, err)
	}
}
